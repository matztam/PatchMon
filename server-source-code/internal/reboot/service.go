package reboot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/notifications"
)

// Request sources.
const (
	SourceManual = "manual"
	SourcePolicy = "policy"
)

// Request statuses. queued, sent and rebooting are in flight.
const (
	StatusQueued    = "queued"
	StatusSent      = "sent"
	StatusRebooting = "rebooting"
	StatusCompleted = "completed"
	StatusRejected  = "rejected"
	StatusFailed    = "failed"
	StatusTimedOut  = "timed_out"
)

// Policy reboot modes.
const (
	ModeNever      = "never"
	ModeIfRequired = "if_required"
)

const (
	MaxDelayMinutes  = 60
	MaxMessageLength = 200
)

var (
	ErrHostNotFound    = errors.New("host not found")
	ErrRequestNotFound = errors.New("reboot request not found")
	ErrActive          = errors.New("a reboot is already in progress for this host")
	ErrNotEnabled      = errors.New("reboot is not enabled on this host")
	ErrNotReported     = errors.New("host has not reported whether reboots are enabled; the agent may be too old")
	ErrInvalidStatus   = errors.New("invalid status")
)

// Enqueue dispatches a queued request to the host's agent.
type Enqueue func(ctx context.Context, apiID, requestID string) error

// Notifier emits notification events; nil disables notifications.
type Notifier func(ctx context.Context, d *database.DB, ev notifications.Event)

// CreateParams describes a new reboot request.
type CreateParams struct {
	HostID        string
	Source        string
	PatchPolicyID *string
	UserID        *string
	DelayMinutes  int
	Message       string
}

// CreateRequest records a queued request for a host that has reported reboot
// support, then hands it to enqueue. A failed enqueue fails the request.
func CreateRequest(ctx context.Context, d *database.DB, p CreateParams, enqueue Enqueue) (*db.RebootRequest, error) {
	host, err := d.Queries.GetHostByID(ctx, p.HostID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrHostNotFound
		}
		return nil, err
	}
	state, err := d.Queries.GetHostRebootState(ctx, p.HostID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotReported
		}
		return nil, err
	}
	if !state.RebootEnabled {
		return nil, ErrNotEnabled
	}

	delay := clamp(p.DelayMinutes, 0, MaxDelayMinutes)
	msg := CleanMessage(p.Message)
	id := uuid.New().String()
	err = d.Queries.CreateRebootRequest(ctx, db.CreateRebootRequestParams{
		ID:                id,
		HostID:            p.HostID,
		Source:            p.Source,
		PatchPolicyID:     p.PatchPolicyID,
		RequestedByUserID: p.UserID,
		DelayMinutes:      int32(delay), //nolint:gosec // clamped to 0..60
		Message:           msg,
		BootTimeBefore:    host.BootTime,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrActive
		}
		return nil, err
	}
	if err := enqueue(ctx, host.ApiID, id); err != nil {
		reason := "could not queue the reboot command: " + err.Error()
		_, _ = d.Queries.FinishRebootRequest(ctx, db.FinishRebootRequestParams{ID: id, Status: StatusFailed, Reason: &reason})
		return nil, fmt.Errorf("enqueue reboot: %w", err)
	}
	req, err := d.Queries.GetRebootRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	return &req, nil
}

// CleanMessage trims the operator message; empty becomes nil.
func CleanMessage(s string) *string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return nil
	}
	if r := []rune(s); len(r) > MaxMessageLength {
		s = string(r[:MaxMessageLength])
	}
	return &s
}

// SetCapability stores whether the host's agent allows reboots.
func SetCapability(ctx context.Context, d *database.DB, hostID string, enabled bool) error {
	return d.Queries.UpsertHostRebootState(ctx, db.UpsertHostRebootStateParams{HostID: hostID, RebootEnabled: enabled})
}

// HandleAck applies the agent's answer to a request it was sent. The request
// must belong to the authenticated host.
func HandleAck(ctx context.Context, d *database.DB, hostID, requestID, status, reason string, notify Notifier) error {
	req, err := d.Queries.GetRebootRequest(ctx, requestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRequestNotFound
		}
		return err
	}
	if req.HostID != hostID {
		return ErrRequestNotFound
	}
	reasonPtr := CleanReason(reason)
	switch status {
	case "accepted":
		n, err := d.Queries.MarkRebootRequestAccepted(ctx, requestID)
		if err != nil || n == 0 {
			return err
		}
		emit(ctx, d, notify, "host_reboot_started", "informational", req, "Reboot started", "is rebooting", reasonPtr)
	case "rejected":
		n, err := d.Queries.FinishRebootRequest(ctx, db.FinishRebootRequestParams{ID: requestID, Status: StatusRejected, Reason: reasonPtr})
		if err != nil || n == 0 {
			return err
		}
		if strings.Contains(reason, "reboot-enabled") {
			_ = SetCapability(ctx, d, hostID, false)
		}
		emit(ctx, d, notify, "host_reboot_rejected", "warning", req, "Reboot rejected", "refused the reboot", reasonPtr)
	case "failed":
		n, err := d.Queries.FinishRebootRequest(ctx, db.FinishRebootRequestParams{ID: requestID, Status: StatusFailed, Reason: reasonPtr})
		if err != nil || n == 0 {
			return err
		}
		emit(ctx, d, notify, "host_reboot_failed", "error", req, "Reboot failed", "could not reboot", reasonPtr)
	default:
		return ErrInvalidStatus
	}
	return nil
}

// CleanReason trims an agent-supplied reason; empty becomes nil.
func CleanReason(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if r := []rune(s); len(r) > 1000 {
		s = string(r[:1000])
	}
	return &s
}

func emit(ctx context.Context, d *database.DB, notify Notifier, typ, severity string, req db.RebootRequest, title, verb string, reason *string) {
	if notify == nil {
		return
	}
	name := req.HostID
	if h, err := d.Queries.GetHostByID(ctx, req.HostID); err == nil {
		name = hostName(h.FriendlyName, h.Hostname)
	}
	msg := fmt.Sprintf("Host %q %s.", name, verb)
	if reason != nil {
		msg += " " + *reason
	}
	meta := map[string]interface{}{
		"host_id":           req.HostID,
		"host_name":         name,
		"reboot_request_id": req.ID,
		"source":            req.Source,
	}
	if req.PatchPolicyID != nil {
		meta["patch_policy_id"] = *req.PatchPolicyID
	}
	notify(ctx, d, notifications.Event{
		Type:          typ,
		Severity:      severity,
		Title:         title + " - " + name,
		Message:       msg,
		ReferenceType: "host",
		ReferenceID:   req.HostID,
		Metadata:      meta,
	})
}

func hostName(friendly string, hostname *string) string {
	if friendly != "" {
		return friendly
	}
	if hostname != nil && *hostname != "" {
		return *hostname
	}
	return "unknown"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Timeouts used by the in-flight check.
const (
	QueuedTimeout = 10 * time.Minute
	SentTimeout   = 5 * time.Minute
	// ComeBackTimeout is how long after the scheduled reboot a host may take
	// to report a new boot time.
	ComeBackTimeout = 20 * time.Minute
	// bootTimeTolerance absorbs jitter in reported boot times.
	bootTimeTolerance = 60 * time.Second
)

// CheckInFlight completes requests whose host reports a new boot time and
// fails those that stalled. It returns the number of requests it changed.
func CheckInFlight(ctx context.Context, d *database.DB, now time.Time, notify Notifier, log *slog.Logger) int {
	rows, err := d.Queries.ListInFlightRebootRequests(ctx)
	if err != nil {
		log.Error("reboot: list in-flight requests failed", "error", err)
		return 0
	}
	changed := 0
	for _, r := range rows {
		req := db.RebootRequest{ID: r.ID, HostID: r.HostID, Source: r.Source}
		switch r.Status {
		case StatusRebooting:
			if rebooted(r.BootTimeBefore, r.HostBootTime, tsOr(r.AcceptedAt, r.CreatedAt)) {
				n, err := d.Queries.CompleteRebootRequest(ctx, db.CompleteRebootRequestParams{ID: r.ID, BootTimeAfter: r.HostBootTime})
				if err == nil && n > 0 {
					changed++
					emit(ctx, d, notify, "host_reboot_completed", "informational", req, "Reboot completed", "is back online", nil)
				}
				continue
			}
			since := tsOr(r.AcceptedAt, r.CreatedAt)
			deadline := since.Add(time.Duration(r.DelayMinutes)*time.Minute + ComeBackTimeout)
			if now.After(deadline) {
				reason := fmt.Sprintf("host did not report a new boot time within %d minutes", int((time.Duration(r.DelayMinutes)*time.Minute+ComeBackTimeout)/time.Minute))
				if finish(ctx, d, r.ID, StatusTimedOut, reason) {
					changed++
					emit(ctx, d, notify, "host_reboot_failed", "error", req, "Reboot timed out", "did not come back", &reason)
				}
			}
		case StatusSent:
			if now.After(tsOr(r.SentAt, r.CreatedAt).Add(SentTimeout)) {
				reason := "the agent did not answer the reboot command (offline, or too old to support reboots)"
				if finish(ctx, d, r.ID, StatusFailed, reason) {
					changed++
					emit(ctx, d, notify, "host_reboot_failed", "error", req, "Reboot failed", "did not answer", &reason)
				}
			}
		case StatusQueued:
			if now.After(tsOr(r.CreatedAt, r.CreatedAt).Add(QueuedTimeout)) {
				reason := "the reboot command could not be delivered (agent not connected)"
				if finish(ctx, d, r.ID, StatusFailed, reason) {
					changed++
					emit(ctx, d, notify, "host_reboot_failed", "error", req, "Reboot failed", "could not be reached", &reason)
				}
			}
		}
	}
	return changed
}

func finish(ctx context.Context, d *database.DB, id, status, reason string) bool {
	n, err := d.Queries.FinishRebootRequest(ctx, db.FinishRebootRequestParams{ID: id, Status: status, Reason: &reason})
	return err == nil && n > 0
}

// rebooted reports whether the host's boot time is a later boot than before.
// Without a previous boot time, a boot after the request was accepted counts.
func rebooted(before, after pgtype.Timestamptz, accepted time.Time) bool {
	if !after.Valid {
		return false
	}
	if !before.Valid {
		return after.Time.After(accepted)
	}
	return after.Time.Sub(before.Time) > bootTimeTolerance
}

func tsOr(t, fallback pgtype.Timestamptz) time.Time {
	if t.Valid {
		return t.Time
	}
	return fallback.Time
}
