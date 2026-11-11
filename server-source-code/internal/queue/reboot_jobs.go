package queue

// fork: host reboot jobs.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hibiken/asynq"

	"github.com/PatchMon/PatchMon/server-source-code/internal/agentregistry"
	"github.com/PatchMon/PatchMon/server-source-code/internal/config"
	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/notifications"
	"github.com/PatchMon/PatchMon/server-source-code/internal/reboot"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

const (
	TypeRebootHost      = "reboot_host"
	TypeRebootScheduler = "reboot_scheduler"
	// RebootSchedulerCron runs the policy scheduler and the in-flight check.
	RebootSchedulerCron = "*/5 * * * *"
)

// RebootHostPayload is the payload of a reboot_host job.
type RebootHostPayload struct {
	ApiID     string `json:"api_id"`
	Host      string `json:"host,omitempty"`
	RequestID string `json:"request_id"`
}

// NewRebootHostTask creates a reboot_host task. It is not retried: a reboot
// command must not be delivered twice.
func NewRebootHostTask(apiID, host, requestID string) (*asynq.Task, error) {
	payload, err := json.Marshal(RebootHostPayload{ApiID: apiID, Host: host, RequestID: requestID})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeRebootHost, payload, asynq.Queue(QueueAgentCommands), asynq.MaxRetry(0)), nil
}

// RebootEnqueuer returns a reboot.Enqueue that queues reboot_host tasks for tenantHost.
func RebootEnqueuer(client *asynq.Client, tenantHost string) reboot.Enqueue {
	return func(_ context.Context, apiID, requestID string) error {
		task, err := NewRebootHostTask(apiID, tenantHost, requestID)
		if err != nil {
			return err
		}
		_, err = client.Enqueue(task)
		return err
	}
}

// RebootNotifier adapts the notification emitter for tenantHost.
func RebootNotifier(emit *notifications.Emitter, tenantHost string) reboot.Notifier {
	if emit == nil {
		return nil
	}
	return func(ctx context.Context, d *database.DB, ev notifications.Event) {
		emit.EmitEvent(ctx, d, tenantHost, ev)
	}
}

// RebootHostHandler delivers a queued reboot request to the agent.
type RebootHostHandler struct {
	registry  *agentregistry.Registry
	db        *database.DB
	poolCache *hostctx.PoolCache
	log       *slog.Logger
}

// NewRebootHostHandler creates a reboot_host handler.
func NewRebootHostHandler(registry *agentregistry.Registry, d *database.DB, poolCache *hostctx.PoolCache, log *slog.Logger) *RebootHostHandler {
	return &RebootHostHandler{registry: registry, db: d, poolCache: poolCache, log: log}
}

// ProcessTask implements asynq.Handler.
func (h *RebootHostHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var p RebootHostPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return err
	}
	ctx, d, err := resolveDBForHost(ctx, p.Host, h.db, h.poolCache)
	if err != nil || d == nil {
		return err
	}
	req, err := d.Queries.GetRebootRequest(ctx, p.RequestID)
	if err != nil || req.Status != reboot.StatusQueued {
		return nil // gone or already handled
	}

	taskID, _ := asynq.GetTaskID(ctx)
	if taskID != "" {
		apiID := p.ApiID
		_ = d.Queries.InsertJobHistory(ctx, db.InsertJobHistoryParams{
			ID:            uuid.New().String(),
			JobID:         taskID,
			QueueName:     QueueAgentCommands,
			JobName:       TypeRebootHost,
			HostID:        &req.HostID,
			ApiID:         &apiID,
			Status:        "active",
			AttemptNumber: 1,
		})
	}
	fail := func(reason string) error {
		_, _ = d.Queries.FinishRebootRequest(ctx, db.FinishRebootRequestParams{ID: req.ID, Status: reboot.StatusFailed, Reason: &reason})
		if taskID != "" {
			_ = d.Queries.UpdateJobHistoryFailed(ctx, db.UpdateJobHistoryFailedParams{JobID: taskID, ErrorMessage: &reason})
		}
		h.log.Warn("reboot_host: "+reason, "api_id", p.ApiID, "reboot_request_id", req.ID)
		return nil
	}

	if !h.registry.IsConnected(p.ApiID) {
		return fail("agent not connected")
	}
	msg := ""
	if req.Message != nil {
		msg = *req.Message
	}
	body, err := json.Marshal(map[string]interface{}{
		"type":          "reboot",
		"request_id":    req.ID,
		"delay_minutes": req.DelayMinutes,
		"message":       msg,
	})
	if err != nil {
		return fail("could not encode reboot command")
	}
	// Mark sent first: the agent may answer before SendMessage returns.
	if _, err := d.Queries.MarkRebootRequestSent(ctx, req.ID); err != nil {
		return fail("could not update reboot request")
	}
	if err := h.registry.SendMessage(p.ApiID, websocket.TextMessage, body); err != nil {
		return fail("could not send reboot command to agent")
	}
	if taskID != "" {
		_ = d.Queries.UpdateJobHistoryCompleted(ctx, taskID)
	}
	h.log.Info("reboot_host sent", "api_id", p.ApiID, "reboot_request_id", req.ID, "delay_minutes", req.DelayMinutes)
	return nil
}

// RebootSchedulerHandler runs the reboot scheduler for every database.
type RebootSchedulerHandler struct {
	registry  *agentregistry.Registry
	db        *database.DB
	poolCache *hostctx.PoolCache
	client    *asynq.Client
	emit      *notifications.Emitter
	log       *slog.Logger
}

// NewRebootSchedulerHandler creates a reboot_scheduler handler.
func NewRebootSchedulerHandler(registry *agentregistry.Registry, d *database.DB, poolCache *hostctx.PoolCache, client *asynq.Client, emit *notifications.Emitter, log *slog.Logger) *RebootSchedulerHandler {
	return &RebootSchedulerHandler{registry: registry, db: d, poolCache: poolCache, client: client, emit: emit, log: log}
}

// ProcessTask implements asynq.Handler.
func (h *RebootSchedulerHandler) ProcessTask(ctx context.Context, _ *asynq.Task) error {
	if h.client == nil {
		return nil
	}
	now := time.Now()
	forEachDB(ctx, h.db, h.poolCache, func(ctx context.Context, d *database.DB, tenantHost string) {
		loc := time.UTC
		if s, err := store.NewSettingsStore(d).GetFirst(ctx); err == nil && s != nil {
			if l, err := time.LoadLocation(config.ResolveTimezone(s.Timezone, nil)); err == nil {
				loc = l
			}
		}
		started, err := reboot.RunScheduler(ctx, d, reboot.SchedulerDeps{
			Now:         now,
			Location:    loc,
			IsConnected: h.registry.IsConnected,
			Enqueue:     RebootEnqueuer(h.client, tenantHost),
			Notify:      RebootNotifier(h.emit, tenantHost),
			Log:         h.log,
		})
		if err != nil {
			h.log.Error("reboot scheduler failed", "tenant", tenantHost, "error", err)
		} else if started > 0 {
			h.log.Info("reboot scheduler started reboots", "tenant", tenantHost, "count", started)
		}
	})
	return nil
}
