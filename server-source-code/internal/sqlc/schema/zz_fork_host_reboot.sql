-- Mirror of internal/migrate/fork_migrations for sqlc. Keep in sync.
-- fork: host reboot. Lives in its own migration track (fork_schema_migrations)
-- so upstream migration numbers never collide with it.

CREATE TABLE IF NOT EXISTS host_reboot_state (
    host_id TEXT PRIMARY KEY REFERENCES hosts(id) ON DELETE CASCADE,
    reboot_enabled BOOLEAN NOT NULL DEFAULT false,
    reported_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS patch_policy_reboot_settings (
    patch_policy_id TEXT PRIMARY KEY REFERENCES patch_policies(id) ON DELETE CASCADE,
    reboot_mode TEXT NOT NULL DEFAULT 'never' CHECK (reboot_mode IN ('never', 'if_required')),
    window_days INTEGER[] NOT NULL DEFAULT '{0,1,2,3,4,5,6}',
    window_start TEXT NOT NULL DEFAULT '02:00',
    window_end TEXT NOT NULL DEFAULT '05:00',
    delay_minutes INTEGER NOT NULL DEFAULT 5 CHECK (delay_minutes BETWEEN 0 AND 60),
    max_concurrent INTEGER NOT NULL DEFAULT 1 CHECK (max_concurrent BETWEEN 1 AND 100),
    paused BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reboot_requests (
    id TEXT PRIMARY KEY,
    host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK (source IN ('manual', 'policy')),
    patch_policy_id TEXT REFERENCES patch_policies(id) ON DELETE SET NULL,
    requested_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'sent', 'rebooting', 'completed', 'rejected', 'failed', 'timed_out')),
    delay_minutes INTEGER NOT NULL DEFAULT 0,
    message TEXT,
    reason TEXT,
    boot_time_before TIMESTAMPTZ,
    boot_time_after TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- At most one request in flight per host.
CREATE UNIQUE INDEX IF NOT EXISTS reboot_requests_one_active_per_host
    ON reboot_requests (host_id) WHERE status IN ('queued', 'sent', 'rebooting');
CREATE INDEX IF NOT EXISTS reboot_requests_host_created
    ON reboot_requests (host_id, created_at DESC);
CREATE INDEX IF NOT EXISTS reboot_requests_policy_active
    ON reboot_requests (patch_policy_id) WHERE status IN ('queued', 'sent', 'rebooting');
