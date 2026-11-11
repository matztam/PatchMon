package reboot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/notifications"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtest"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixture struct {
	t   *testing.T
	ctx context.Context
	d   *database.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := pgtest.Open(t)
	f := &fixture{t: t, ctx: context.Background(), d: d}
	f.exec(`TRUNCATE hosts, patch_policies, host_groups CASCADE`)
	return f
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.d.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

// host inserts a host that needs a reboot, booted bootTime ago.
func (f *fixture) host(name string, needsReboot bool, rebootEnabled *bool) string {
	f.t.Helper()
	id := uuid.NewString()
	f.exec(`INSERT INTO hosts (id, friendly_name, os_type, os_version, updated_at, api_id, api_key, needs_reboot, reboot_reason, boot_time)
		VALUES ($1, $2, 'linux', '13', NOW(), $3, $4, $5, 'kernel update', NOW() - INTERVAL '3 days')`,
		id, name, "api-"+id, "key-"+id, needsReboot)
	if rebootEnabled != nil {
		if err := SetCapability(f.ctx, f.d, id, *rebootEnabled); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

func (f *fixture) policy(name string, settings db.UpsertPolicyRebootSettingsParams) string {
	f.t.Helper()
	id := uuid.NewString()
	f.exec(`INSERT INTO patch_policies (id, name, patch_delay_type, updated_at) VALUES ($1, $2, 'immediate', NOW())`, id, name)
	settings.PatchPolicyID = id
	if err := f.d.Queries.UpsertPolicyRebootSettings(f.ctx, settings); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) assignToGroup(policyID string, hostIDs ...string) string {
	f.t.Helper()
	gid := uuid.NewString()
	f.exec(`INSERT INTO host_groups (id, name, updated_at) VALUES ($1, $2, NOW())`, gid, "g-"+gid)
	for _, h := range hostIDs {
		f.exec(`INSERT INTO host_group_memberships (id, host_id, host_group_id) VALUES ($1, $2, $3)`, uuid.NewString(), h, gid)
	}
	f.exec(`INSERT INTO patch_policy_assignments (id, patch_policy_id, target_type, target_id, updated_at) VALUES ($1, $2, 'host_group', $3, NOW())`,
		uuid.NewString(), policyID, gid)
	return gid
}

type enqueueRecorder struct {
	mu   sync.Mutex
	reqs []string
	err  error
}

func (e *enqueueRecorder) fn(_ context.Context, _, requestID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.reqs = append(e.reqs, requestID)
	return nil
}

type notifyRecorder struct{ types []string }

func (n *notifyRecorder) fn(_ context.Context, _ *database.DB, ev notifications.Event) {
	n.types = append(n.types, ev.Type)
}

func ptr[T any](v T) *T { return &v }

func autoPolicy(maxConcurrent int32) db.UpsertPolicyRebootSettingsParams {
	return db.UpsertPolicyRebootSettingsParams{
		RebootMode: ModeIfRequired, WindowDays: []int32{0, 1, 2, 3, 4, 5, 6},
		WindowStart: "00:00", WindowEnd: "00:00", DelayMinutes: 5, MaxConcurrent: maxConcurrent,
	}
}

func TestCreateRequestChecksCapability(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}

	unknown := f.host("unknown", true, nil)
	if _, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: unknown, Source: SourceManual}, enq.fn); !errors.Is(err, ErrNotReported) {
		t.Fatalf("unreported host: %v", err)
	}
	disabled := f.host("disabled", true, ptr(false))
	if _, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: disabled, Source: SourceManual}, enq.fn); !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("disabled host: %v", err)
	}
	if _, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: uuid.NewString(), Source: SourceManual}, enq.fn); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("missing host: %v", err)
	}
	if len(enq.reqs) != 0 {
		t.Fatalf("enqueued %v", enq.reqs)
	}
}

func TestCreateRequestOneActivePerHost(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}
	h := f.host("web", true, ptr(true))

	req, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: h, Source: SourceManual, DelayMinutes: 99, Message: " hi "}, enq.fn)
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != StatusQueued || req.DelayMinutes != MaxDelayMinutes || *req.Message != "hi" || !req.BootTimeBefore.Valid {
		t.Fatalf("request = %+v", req)
	}
	if _, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: h, Source: SourceManual}, enq.fn); !errors.Is(err, ErrActive) {
		t.Fatalf("second request: %v", err)
	}
	if len(enq.reqs) != 1 || enq.reqs[0] != req.ID {
		t.Fatalf("enqueued %v", enq.reqs)
	}
}

func TestCreateRequestFailsWhenEnqueueFails(t *testing.T) {
	f := newFixture(t)
	h := f.host("web", true, ptr(true))
	enq := &enqueueRecorder{err: errors.New("redis down")}

	if _, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: h, Source: SourceManual}, enq.fn); err == nil {
		t.Fatal("expected error")
	}
	if _, err := f.d.Queries.GetActiveRebootRequestForHost(f.ctx, h); err == nil {
		t.Fatal("a failed enqueue left an active request behind")
	}
}

func TestAckLifecycle(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}
	notes := &notifyRecorder{}
	h := f.host("web", true, ptr(true))
	other := f.host("other", true, ptr(true))

	req, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: h, Source: SourceManual, DelayMinutes: 1}, enq.fn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Queries.MarkRebootRequestSent(f.ctx, req.ID); err != nil {
		t.Fatal(err)
	}
	// Another host cannot answer for it.
	if err := HandleAck(f.ctx, f.d, other, req.ID, "accepted", "", notes.fn); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("foreign ack: %v", err)
	}
	if err := HandleAck(f.ctx, f.d, h, req.ID, "bogus", "", notes.fn); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("bogus status: %v", err)
	}
	if err := HandleAck(f.ctx, f.d, h, req.ID, "accepted", "", notes.fn); err != nil {
		t.Fatal(err)
	}
	got, _ := f.d.Queries.GetRebootRequest(f.ctx, req.ID)
	if got.Status != StatusRebooting {
		t.Fatalf("status = %s", got.Status)
	}

	// Not back yet: nothing changes.
	if n := CheckInFlight(f.ctx, f.d, time.Now(), notes.fn, quietLog); n != 0 {
		t.Fatalf("changed %d before reboot", n)
	}
	// The host reports a new boot time.
	f.exec(`UPDATE hosts SET boot_time = NOW() WHERE id = $1`, h)
	if n := CheckInFlight(f.ctx, f.d, time.Now(), notes.fn, quietLog); n != 1 {
		t.Fatalf("changed %d after reboot", n)
	}
	got, _ = f.d.Queries.GetRebootRequest(f.ctx, req.ID)
	if got.Status != StatusCompleted || !got.BootTimeAfter.Valid {
		t.Fatalf("request = %+v", got)
	}
	want := []string{"host_reboot_started", "host_reboot_completed"}
	if len(notes.types) != 2 || notes.types[0] != want[0] || notes.types[1] != want[1] {
		t.Fatalf("notifications = %v", notes.types)
	}
}

func TestRejectedAckForDisabledHostClearsCapability(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}
	h := f.host("web", true, ptr(true))
	req, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: h, Source: SourceManual}, enq.fn)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleAck(f.ctx, f.d, h, req.ID, "rejected", "Reboot is not enabled on this host ... reboot-enabled: true", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := f.d.Queries.GetRebootRequest(f.ctx, req.ID)
	st, _ := f.d.Queries.GetHostRebootState(f.ctx, h)
	if got.Status != StatusRejected || st.RebootEnabled {
		t.Fatalf("status=%s enabled=%v", got.Status, st.RebootEnabled)
	}
}

func TestCheckInFlightTimeouts(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}
	notes := &notifyRecorder{}
	queued := f.host("queued", true, ptr(true))
	sent := f.host("sent", true, ptr(true))
	stuck := f.host("stuck", true, ptr(true))

	mk := func(h string) string {
		r, err := CreateRequest(f.ctx, f.d, CreateParams{HostID: h, Source: SourceManual, DelayMinutes: 5}, enq.fn)
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	qID, sID, rID := mk(queued), mk(sent), mk(stuck)
	_, _ = f.d.Queries.MarkRebootRequestSent(f.ctx, sID)
	_, _ = f.d.Queries.MarkRebootRequestSent(f.ctx, rID)
	_, _ = f.d.Queries.MarkRebootRequestAccepted(f.ctx, rID)

	// Within every limit nothing happens.
	if n := CheckInFlight(f.ctx, f.d, time.Now().Add(time.Minute), notes.fn, quietLog); n != 0 {
		t.Fatalf("changed %d too early", n)
	}
	// 30 minutes later all three are overdue (5 min delay + 20 min to come back).
	if n := CheckInFlight(f.ctx, f.d, time.Now().Add(30*time.Minute), notes.fn, quietLog); n != 3 {
		t.Fatalf("changed %d, want 3", n)
	}
	for id, want := range map[string]string{qID: StatusFailed, sID: StatusFailed, rID: StatusTimedOut} {
		got, _ := f.d.Queries.GetRebootRequest(f.ctx, id)
		if got.Status != want {
			t.Errorf("%s: status %s, want %s", id, got.Status, want)
		}
	}
}

func TestSchedulerStaggersAndRespectsRules(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}
	pol := f.policy("auto", autoPolicy(1))

	a := f.host("a", true, ptr(true))
	b := f.host("b", true, ptr(true))
	notNeeded := f.host("fine", false, ptr(true))
	disabled := f.host("disabled", true, ptr(false))
	failedPatch := f.host("failed", true, ptr(true))
	excluded := f.host("excluded", true, ptr(true))
	offline := f.host("offline", true, ptr(true))
	f.assignToGroup(pol, a, b, notNeeded, disabled, failedPatch, excluded, offline)
	f.exec(`INSERT INTO patch_policy_exclusions (id, patch_policy_id, host_id, updated_at) VALUES ($1, $2, $3, NOW())`, uuid.NewString(), pol, excluded)
	// A failed patch run since the last boot blocks the automatic reboot.
	f.exec(`INSERT INTO patch_runs (id, host_id, job_id, patch_type, status, completed_at, updated_at)
		VALUES ($1, $2, 'j', 'all', 'failed', NOW() AT TIME ZONE 'UTC', NOW() AT TIME ZONE 'UTC')`, uuid.NewString(), failedPatch)
	// An unassigned host is not touched.
	f.host("unassigned", true, ptr(true))

	deps := SchedulerDeps{
		Now: time.Now(), Location: time.UTC, Enqueue: enq.fn, Log: quietLog,
		IsConnected: func(apiID string) bool { return apiID != "api-"+offline },
	}
	started, err := RunScheduler(f.ctx, f.d, deps)
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("started %d, want 1 (max_concurrent)", started)
	}
	first, err := f.d.Queries.GetRebootRequest(f.ctx, enq.reqs[0])
	if err != nil || (first.HostID != a && first.HostID != b) || first.Source != SourcePolicy || *first.PatchPolicyID != pol || first.DelayMinutes != 5 {
		t.Fatalf("first = %+v, %v", first, err)
	}

	// While it is in flight the next run starts nothing.
	if started, _ := RunScheduler(f.ctx, f.d, deps); started != 0 {
		t.Fatalf("started %d while one is in flight", started)
	}

	// Once the first host is back, the second one goes.
	_, _ = f.d.Queries.MarkRebootRequestSent(f.ctx, first.ID)
	_, _ = f.d.Queries.MarkRebootRequestAccepted(f.ctx, first.ID)
	f.exec(`UPDATE hosts SET boot_time = NOW(), needs_reboot = false WHERE id = $1`, first.HostID)
	if started, _ := RunScheduler(f.ctx, f.d, deps); started != 1 {
		t.Fatalf("started %d after the first came back", started)
	}
	second, _ := f.d.Queries.GetRebootRequest(f.ctx, enq.reqs[1])
	if second.HostID == first.HostID || (second.HostID != a && second.HostID != b) {
		t.Fatalf("second = %+v", second)
	}
	if len(enq.reqs) != 2 {
		t.Fatalf("enqueued %d requests, want 2", len(enq.reqs))
	}
}

func TestSchedulerHonoursWindowAndPause(t *testing.T) {
	f := newFixture(t)
	enq := &enqueueRecorder{}
	closed := autoPolicy(5)
	closed.WindowStart, closed.WindowEnd = "02:00", "03:00"
	pol := f.policy("night", closed)
	paused := autoPolicy(5)
	paused.Paused = true
	pausedPol := f.policy("paused", paused)
	f.assignToGroup(pol, f.host("a", true, ptr(true)))
	f.assignToGroup(pausedPol, f.host("b", true, ptr(true)))

	noon := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	deps := SchedulerDeps{Now: noon, Location: time.UTC, Enqueue: enq.fn, Log: quietLog}
	if started, _ := RunScheduler(f.ctx, f.d, deps); started != 0 {
		t.Fatalf("started %d outside the window", started)
	}
	deps.Now = time.Date(2026, 11, 8, 2, 30, 0, 0, time.UTC)
	if started, _ := RunScheduler(f.ctx, f.d, deps); started != 1 {
		t.Fatalf("started %d inside the window, want 1 (paused policy skipped)", started)
	}
}
