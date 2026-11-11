-- fork: host reboot

-- name: UpsertHostRebootState :exec
INSERT INTO host_reboot_state (host_id, reboot_enabled, reported_at)
VALUES ($1, $2, NOW())
ON CONFLICT (host_id) DO UPDATE SET reboot_enabled = EXCLUDED.reboot_enabled, reported_at = NOW();

-- name: GetHostRebootState :one
SELECT * FROM host_reboot_state WHERE host_id = $1;

-- name: GetPolicyRebootSettings :one
SELECT * FROM patch_policy_reboot_settings WHERE patch_policy_id = $1;

-- name: UpsertPolicyRebootSettings :exec
INSERT INTO patch_policy_reboot_settings (
    patch_policy_id, reboot_mode, window_days, window_start, window_end,
    delay_minutes, max_concurrent, paused, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
ON CONFLICT (patch_policy_id) DO UPDATE SET
    reboot_mode = EXCLUDED.reboot_mode,
    window_days = EXCLUDED.window_days,
    window_start = EXCLUDED.window_start,
    window_end = EXCLUDED.window_end,
    delay_minutes = EXCLUDED.delay_minutes,
    max_concurrent = EXCLUDED.max_concurrent,
    paused = EXCLUDED.paused,
    updated_at = NOW();

-- name: ListPolicyRebootSettings :many
SELECT * FROM patch_policy_reboot_settings;

-- name: ListAutoRebootPolicies :many
SELECT s.* FROM patch_policy_reboot_settings s
JOIN patch_policies p ON p.id = s.patch_policy_id
WHERE s.reboot_mode = 'if_required' AND NOT s.paused;

-- name: CreateRebootRequest :exec
INSERT INTO reboot_requests (
    id, host_id, source, patch_policy_id, requested_by_user_id, status,
    delay_minutes, message, boot_time_before
) VALUES ($1, $2, $3, $4, $5, 'queued', $6, $7, $8);

-- name: GetRebootRequest :one
SELECT * FROM reboot_requests WHERE id = $1;

-- name: GetActiveRebootRequestForHost :one
SELECT * FROM reboot_requests
WHERE host_id = $1 AND status IN ('queued', 'sent', 'rebooting')
LIMIT 1;

-- name: ListRebootRequestsForHost :many
SELECT r.*, u.username AS requested_by_username
FROM reboot_requests r
LEFT JOIN users u ON u.id = r.requested_by_user_id
WHERE r.host_id = $1
ORDER BY r.created_at DESC
LIMIT $2;

-- name: MarkRebootRequestSent :execrows
UPDATE reboot_requests SET status = 'sent', sent_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'queued';

-- name: MarkRebootRequestAccepted :execrows
UPDATE reboot_requests SET status = 'rebooting', accepted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status IN ('queued', 'sent');

-- name: FinishRebootRequest :execrows
-- Ends a request that is still in flight as rejected, failed or timed_out.
UPDATE reboot_requests SET status = sqlc.arg('status'), reason = sqlc.narg('reason'), completed_at = NOW(), updated_at = NOW()
WHERE id = sqlc.arg('id') AND status IN ('queued', 'sent', 'rebooting');

-- name: CompleteRebootRequest :execrows
UPDATE reboot_requests SET status = 'completed', boot_time_after = $2, completed_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'rebooting';

-- name: CountActiveRebootsForPolicy :one
SELECT COUNT(*) FROM reboot_requests
WHERE patch_policy_id = $1 AND status IN ('queued', 'sent', 'rebooting');

-- name: ListInFlightRebootRequests :many
SELECT r.id, r.host_id, r.status, r.source, r.delay_minutes, r.boot_time_before,
       r.sent_at, r.accepted_at, r.created_at,
       h.boot_time AS host_boot_time, h.friendly_name AS host_friendly_name, h.hostname AS host_hostname
FROM reboot_requests r
JOIN hosts h ON h.id = r.host_id
WHERE r.status IN ('queued', 'sent', 'rebooting');

-- name: ListRebootCandidateHosts :many
-- Hosts that need a reboot, allow it, have nothing in flight and no failed
-- patch run since they last booted.
SELECT h.id, h.api_id, h.friendly_name, h.hostname, h.boot_time, h.reboot_reason
FROM hosts h
JOIN host_reboot_state s ON s.host_id = h.id AND s.reboot_enabled
WHERE h.needs_reboot
  AND NOT EXISTS (
      SELECT 1 FROM reboot_requests r
      WHERE r.host_id = h.id AND r.status IN ('queued', 'sent', 'rebooting'))
  AND NOT EXISTS (
      SELECT 1 FROM patch_runs pr
      WHERE pr.host_id = h.id AND pr.status IN ('queued', 'pending_validation', 'running'))
  AND NOT EXISTS (
      SELECT 1 FROM patch_runs pr
      WHERE pr.host_id = h.id AND pr.status = 'failed' AND NOT pr.dry_run
        AND (h.boot_time IS NULL OR COALESCE(pr.completed_at, pr.updated_at) AT TIME ZONE 'UTC' > h.boot_time))
ORDER BY h.friendly_name;
