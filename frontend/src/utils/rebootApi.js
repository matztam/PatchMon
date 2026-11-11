// fork: host reboot API.
import api from "./api";

export const rebootAPI = {
	getHostReboot: (hostId) =>
		api.get(`/hosts/${hostId}/reboot`).then((res) => res.data),
	requestReboot: (hostId, { delayMinutes, message }) =>
		api
			.post(`/hosts/${hostId}/reboot`, {
				delay_minutes: delayMinutes,
				message,
			})
			.then((res) => res.data),
	getPolicyReboot: (policyId) =>
		api.get(`/patching/policies/${policyId}/reboot`).then((res) => res.data),
	savePolicyReboot: (policyId, settings) =>
		api
			.put(`/patching/policies/${policyId}/reboot`, settings)
			.then((res) => res.data),
};

export const REBOOT_ACTIVE_STATUSES = ["queued", "sent", "rebooting"];

export const REBOOT_STATUS_LABELS = {
	queued: "Queued",
	sent: "Sent to agent",
	rebooting: "Rebooting…",
	completed: "Completed",
	rejected: "Rejected by host",
	failed: "Failed",
	timed_out: "Did not come back",
};

export const WEEKDAYS = [
	{ value: 1, label: "Mon" },
	{ value: 2, label: "Tue" },
	{ value: 3, label: "Wed" },
	{ value: 4, label: "Thu" },
	{ value: 5, label: "Fri" },
	{ value: 6, label: "Sat" },
	{ value: 0, label: "Sun" },
];
