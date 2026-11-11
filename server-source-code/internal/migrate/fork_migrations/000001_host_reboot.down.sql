DELETE FROM alert_config WHERE alert_type IN ('host_reboot_started', 'host_reboot_completed', 'host_reboot_failed', 'host_reboot_rejected');
DROP TABLE IF EXISTS reboot_requests;
DROP TABLE IF EXISTS patch_policy_reboot_settings;
DROP TABLE IF EXISTS host_reboot_state;
