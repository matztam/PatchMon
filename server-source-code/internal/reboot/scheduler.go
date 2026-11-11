package reboot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// SchedulerDeps are the collaborators of one scheduler run for one database.
type SchedulerDeps struct {
	Now         time.Time
	Location    *time.Location // timezone of the maintenance windows
	IsConnected func(apiID string) bool
	Enqueue     Enqueue
	Notify      Notifier
	Log         *slog.Logger
}

// RunScheduler checks in-flight requests, then starts reboots for hosts whose
// effective patch policy reboots automatically and whose window is open.
// It returns the number of reboots it started.
func RunScheduler(ctx context.Context, d *database.DB, deps SchedulerDeps) (int, error) {
	CheckInFlight(ctx, d, deps.Now, deps.Notify, deps.Log)

	policies, err := d.Queries.ListAutoRebootPolicies(ctx)
	if err != nil {
		return 0, fmt.Errorf("list auto-reboot policies: %w", err)
	}
	open := map[string]db.PatchPolicyRebootSetting{}
	for _, p := range policies {
		in, err := InWindow(deps.Now, deps.Location, p.WindowDays, p.WindowStart, p.WindowEnd)
		if err != nil {
			deps.Log.Warn("reboot: invalid window on policy", "patch_policy_id", p.PatchPolicyID, "error", err)
			continue
		}
		if in {
			open[p.PatchPolicyID] = p
		}
	}
	if len(open) == 0 {
		return 0, nil
	}

	candidates, err := d.Queries.ListRebootCandidateHosts(ctx)
	if err != nil {
		return 0, fmt.Errorf("list reboot candidates: %w", err)
	}
	policyStore := store.NewPatchPoliciesStore(d)
	byPolicy := map[string][]db.ListRebootCandidateHostsRow{}
	for _, h := range candidates {
		if deps.IsConnected != nil && !deps.IsConnected(h.ApiID) {
			continue
		}
		policy, err := policyStore.ResolveEffectivePolicy(ctx, h.ID)
		if err != nil || policy == nil {
			continue
		}
		if _, ok := open[policy.ID]; ok {
			byPolicy[policy.ID] = append(byPolicy[policy.ID], h)
		}
	}

	started := 0
	for policyID, hosts := range byPolicy {
		settings := open[policyID]
		active, err := d.Queries.CountActiveRebootsForPolicy(ctx, &policyID)
		if err != nil {
			deps.Log.Error("reboot: count active reboots failed", "patch_policy_id", policyID, "error", err)
			continue
		}
		slots := int(settings.MaxConcurrent) - int(active)
		for _, h := range hosts {
			if slots <= 0 {
				break
			}
			pid := policyID
			msg := "Automatic reboot by patch policy"
			if h.RebootReason != nil && *h.RebootReason != "" {
				msg += ": " + *h.RebootReason
			}
			_, err := CreateRequest(ctx, d, CreateParams{
				HostID:        h.ID,
				Source:        SourcePolicy,
				PatchPolicyID: &pid,
				DelayMinutes:  int(settings.DelayMinutes),
				Message:       msg,
			}, deps.Enqueue)
			if err != nil {
				if !errors.Is(err, ErrActive) {
					deps.Log.Warn("reboot: could not start policy reboot", "host_id", h.ID, "patch_policy_id", policyID, "error", err)
				}
				continue
			}
			deps.Log.Info("reboot: policy reboot started", "host_id", h.ID, "patch_policy_id", policyID)
			slots--
			started++
		}
	}
	return started, nil
}
