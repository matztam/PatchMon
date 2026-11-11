package handler

// fork: host reboot endpoints.

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/models"
	"github.com/PatchMon/PatchMon/server-source-code/internal/notifications"
	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
	"github.com/PatchMon/PatchMon/server-source-code/internal/reboot"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
	"github.com/PatchMon/PatchMon/server-source-code/internal/util"
)

// RebootHandler serves manual reboots, policy reboot settings and the agent
// callbacks for reboots.
type RebootHandler struct {
	db            database.DBProvider
	hosts         *store.HostsStore
	patchPolicies *store.PatchPoliciesStore
	queueClient   *asynq.Client
	notify        *notifications.Emitter
	log           *slog.Logger
}

// NewRebootHandler creates a reboot handler.
func NewRebootHandler(dbp database.DBProvider, hosts *store.HostsStore, patchPolicies *store.PatchPoliciesStore, queueClient *asynq.Client, notify *notifications.Emitter, log *slog.Logger) *RebootHandler {
	return &RebootHandler{db: dbp, hosts: hosts, patchPolicies: patchPolicies, queueClient: queueClient, notify: notify, log: log}
}

func (h *RebootHandler) notifier(r *http.Request) reboot.Notifier {
	return queue.RebootNotifier(h.notify, hostctx.TenantHostKey(r.Context()))
}

// RequestReboot handles POST /hosts/{hostId}/reboot.
func (h *RebootHandler) RequestReboot(w http.ResponseWriter, r *http.Request) {
	if h.queueClient == nil {
		Error(w, http.StatusServiceUnavailable, "Queue service unavailable")
		return
	}
	hostID := chi.URLParam(r, "hostId")
	var body struct {
		DelayMinutes int    `json:"delay_minutes"`
		Message      string `json:"message"`
	}
	if err := decodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.DelayMinutes < 0 || body.DelayMinutes > reboot.MaxDelayMinutes {
		Error(w, http.StatusBadRequest, "delay_minutes must be between 0 and 60")
		return
	}
	var userID *string
	if id, _ := r.Context().Value(middleware.UserIDKey).(string); id != "" {
		userID = &id
	}
	d := h.db.DB(r.Context())
	req, err := reboot.CreateRequest(r.Context(), d, reboot.CreateParams{
		HostID:       hostID,
		Source:       reboot.SourceManual,
		UserID:       userID,
		DelayMinutes: body.DelayMinutes,
		Message:      body.Message,
	}, queue.RebootEnqueuer(h.queueClient, hostFromRequest(r)))
	switch {
	case errors.Is(err, reboot.ErrHostNotFound):
		Error(w, http.StatusNotFound, "Host not found")
	case errors.Is(err, reboot.ErrActive):
		Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, reboot.ErrNotEnabled), errors.Is(err, reboot.ErrNotReported):
		Error(w, http.StatusPreconditionFailed, err.Error())
	case err != nil:
		h.log.Error("reboot: create request failed", "host_id", hostID, "error", err)
		Error(w, http.StatusInternalServerError, "Failed to request reboot")
	default:
		JSON(w, http.StatusAccepted, map[string]interface{}{"success": true, "request": rebootRequestMap(*req, nil)})
	}
}

// GetHostReboot handles GET /hosts/{hostId}/reboot.
func (h *RebootHandler) GetHostReboot(w http.ResponseWriter, r *http.Request) {
	hostID := chi.URLParam(r, "hostId")
	d := h.db.DB(r.Context())
	resp := map[string]interface{}{"reboot_enabled": nil, "reported_at": nil, "active": nil}
	if st, err := d.Queries.GetHostRebootState(r.Context(), hostID); err == nil {
		resp["reboot_enabled"] = st.RebootEnabled
		resp["reported_at"] = tsPtr(st.ReportedAt)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusInternalServerError, "Failed to load reboot state")
		return
	}
	rows, err := d.Queries.ListRebootRequestsForHost(r.Context(), db.ListRebootRequestsForHostParams{HostID: hostID, Limit: 10})
	if err != nil {
		Error(w, http.StatusInternalServerError, "Failed to load reboot history")
		return
	}
	history := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		m := rebootRequestMap(db.RebootRequest{
			ID: row.ID, HostID: row.HostID, Source: row.Source, PatchPolicyID: row.PatchPolicyID,
			RequestedByUserID: row.RequestedByUserID, Status: row.Status, DelayMinutes: row.DelayMinutes,
			Message: row.Message, Reason: row.Reason, BootTimeBefore: row.BootTimeBefore,
			BootTimeAfter: row.BootTimeAfter, CreatedAt: row.CreatedAt, SentAt: row.SentAt,
			AcceptedAt: row.AcceptedAt, CompletedAt: row.CompletedAt, UpdatedAt: row.UpdatedAt,
		}, row.RequestedByUsername)
		history = append(history, m)
		if resp["active"] == nil && slices.Contains([]string{reboot.StatusQueued, reboot.StatusSent, reboot.StatusRebooting}, row.Status) {
			resp["active"] = m
		}
	}
	resp["history"] = history
	JSON(w, http.StatusOK, resp)
}

func rebootRequestMap(r db.RebootRequest, username *string) map[string]interface{} {
	return map[string]interface{}{
		"id":                    r.ID,
		"host_id":               r.HostID,
		"source":                r.Source,
		"patch_policy_id":       r.PatchPolicyID,
		"requested_by_username": username,
		"status":                r.Status,
		"delay_minutes":         r.DelayMinutes,
		"message":               r.Message,
		"reason":                r.Reason,
		"created_at":            tsPtr(r.CreatedAt),
		"sent_at":               tsPtr(r.SentAt),
		"accepted_at":           tsPtr(r.AcceptedAt),
		"completed_at":          tsPtr(r.CompletedAt),
	}
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

type policyRebootSettings struct {
	RebootMode    string  `json:"reboot_mode"`
	WindowDays    []int32 `json:"window_days"`
	WindowStart   string  `json:"window_start"`
	WindowEnd     string  `json:"window_end"`
	DelayMinutes  int32   `json:"delay_minutes"`
	MaxConcurrent int32   `json:"max_concurrent"`
	Paused        bool    `json:"paused"`
}

func defaultPolicyRebootSettings() policyRebootSettings {
	return policyRebootSettings{
		RebootMode: reboot.ModeNever, WindowDays: []int32{0, 1, 2, 3, 4, 5, 6},
		WindowStart: "02:00", WindowEnd: "05:00", DelayMinutes: 5, MaxConcurrent: 1,
	}
}

func (h *RebootHandler) policyExists(r *http.Request, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	p, err := h.patchPolicies.GetByID(r.Context(), id)
	return err == nil && p != nil
}

// GetPolicyReboot handles GET /patching/policies/{id}/reboot.
func (h *RebootHandler) GetPolicyReboot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.policyExists(r, id) {
		Error(w, http.StatusNotFound, "Policy not found")
		return
	}
	s, err := h.db.DB(r.Context()).Queries.GetPolicyRebootSettings(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		JSON(w, http.StatusOK, defaultPolicyRebootSettings())
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "Failed to load reboot settings")
		return
	}
	JSON(w, http.StatusOK, policyRebootSettings{
		RebootMode: s.RebootMode, WindowDays: s.WindowDays, WindowStart: s.WindowStart, WindowEnd: s.WindowEnd,
		DelayMinutes: s.DelayMinutes, MaxConcurrent: s.MaxConcurrent, Paused: s.Paused,
	})
}

// PutPolicyReboot handles PUT /patching/policies/{id}/reboot.
func (h *RebootHandler) PutPolicyReboot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.policyExists(r, id) {
		Error(w, http.StatusNotFound, "Policy not found")
		return
	}
	body := defaultPolicyRebootSettings()
	if err := decodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if msg := validatePolicyReboot(body); msg != "" {
		Error(w, http.StatusBadRequest, msg)
		return
	}
	slices.Sort(body.WindowDays)
	body.WindowDays = slices.Compact(body.WindowDays)
	err := h.db.DB(r.Context()).Queries.UpsertPolicyRebootSettings(r.Context(), db.UpsertPolicyRebootSettingsParams{
		PatchPolicyID: id, RebootMode: body.RebootMode, WindowDays: body.WindowDays,
		WindowStart: body.WindowStart, WindowEnd: body.WindowEnd, DelayMinutes: body.DelayMinutes,
		MaxConcurrent: body.MaxConcurrent, Paused: body.Paused,
	})
	if err != nil {
		h.log.Error("reboot: save policy settings failed", "patch_policy_id", id, "error", err)
		Error(w, http.StatusInternalServerError, "Failed to save reboot settings")
		return
	}
	JSON(w, http.StatusOK, body)
}

func validatePolicyReboot(s policyRebootSettings) string {
	if s.RebootMode != reboot.ModeNever && s.RebootMode != reboot.ModeIfRequired {
		return "reboot_mode must be never or if_required"
	}
	if err := reboot.ValidateDays(s.WindowDays); err != nil {
		return err.Error()
	}
	if _, err := reboot.ParseClock(s.WindowStart); err != nil {
		return "window_start: " + err.Error()
	}
	if _, err := reboot.ParseClock(s.WindowEnd); err != nil {
		return "window_end: " + err.Error()
	}
	if s.DelayMinutes < 0 || s.DelayMinutes > reboot.MaxDelayMinutes {
		return "delay_minutes must be between 0 and 60"
	}
	if s.MaxConcurrent < 1 || s.MaxConcurrent > 100 {
		return "max_concurrent must be between 1 and 100"
	}
	return ""
}

// authenticateAgent checks the agent's API credentials.
func (h *RebootHandler) authenticateAgent(w http.ResponseWriter, r *http.Request) (*models.Host, bool) {
	apiID := r.Header.Get("X-API-ID")
	apiKey := r.Header.Get("X-API-KEY")
	if apiID == "" || apiKey == "" {
		JSON(w, http.StatusUnauthorized, map[string]string{"error": "API ID and Key required"})
		return nil, false
	}
	host, err := h.hosts.GetByApiID(r.Context(), apiID)
	if err != nil || host == nil {
		JSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid API credentials"})
		return nil, false
	}
	if ok, err := util.VerifyAPIKey(apiKey, host.ApiKey); err != nil || !ok {
		JSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid API credentials"})
		return nil, false
	}
	return host, true
}

// AgentCapability handles POST /hosts/reboot/capability (agent).
func (h *RebootHandler) AgentCapability(w http.ResponseWriter, r *http.Request) {
	host, ok := h.authenticateAgent(w, r)
	if !ok {
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}
	if err := reboot.SetCapability(r.Context(), h.db.DB(r.Context()), host.ID, body.Enabled); err != nil {
		h.log.Error("reboot: save capability failed", "host_id", host.ID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save"})
		return
	}
	JSON(w, http.StatusOK, map[string]bool{"success": true})
}

// AgentAck handles POST /hosts/reboot/requests/{requestId}/ack (agent).
func (h *RebootHandler) AgentAck(w http.ResponseWriter, r *http.Request) {
	host, ok := h.authenticateAgent(w, r)
	if !ok {
		return
	}
	requestID := chi.URLParam(r, "requestId")
	if _, err := uuid.Parse(requestID); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request ID"})
		return
	}
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}
	err := reboot.HandleAck(r.Context(), h.db.DB(r.Context()), host.ID, requestID, body.Status, body.Reason, h.notifier(r))
	switch {
	case errors.Is(err, reboot.ErrRequestNotFound):
		JSON(w, http.StatusNotFound, map[string]string{"error": "Reboot request not found"})
	case errors.Is(err, reboot.ErrInvalidStatus):
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid status"})
	case err != nil:
		h.log.Error("reboot: ack failed", "host_id", host.ID, "reboot_request_id", requestID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to record ack"})
	default:
		JSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}
