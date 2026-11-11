// fork: host reboot button with confirmation dialog and live status.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Power, RotateCw } from "lucide-react";
import { useState } from "react";
import { useToast } from "../../contexts/ToastContext";
import {
	REBOOT_ACTIVE_STATUSES,
	REBOOT_STATUS_LABELS,
	rebootAPI,
} from "../../utils/rebootApi";
import ModalPortal from "../ui/ModalPortal";

const DELAYS = [0, 1, 5, 15];

const disabledHint =
	"Reboot is not enabled on this host. Set integrations: reboot-enabled: true in the agent's config.yml and restart the agent.";

const RebootHostButton = ({ host, connected }) => {
	const toast = useToast();
	const queryClient = useQueryClient();
	const [open, setOpen] = useState(false);
	const [delay, setDelay] = useState(1);
	const [message, setMessage] = useState("");
	const hostId = host?.id;

	const { data } = useQuery({
		queryKey: ["host-reboot", hostId],
		queryFn: () => rebootAPI.getHostReboot(hostId),
		enabled: !!hostId,
		refetchInterval: (query) => (query.state.data?.active ? 10000 : false),
	});

	const mutation = useMutation({
		mutationFn: () =>
			rebootAPI.requestReboot(hostId, { delayMinutes: delay, message }),
		onSuccess: () => {
			setOpen(false);
			setMessage("");
			toast.success(
				delay === 0
					? "Reboot requested"
					: `Reboot requested in ${delay} minute${delay === 1 ? "" : "s"}`,
			);
			queryClient.invalidateQueries({ queryKey: ["host-reboot", hostId] });
		},
		onError: (err) => {
			toast.error(err.response?.data?.error || "Failed to request reboot");
			queryClient.invalidateQueries({ queryKey: ["host-reboot", hostId] });
		},
	});

	if (!host) return null;

	const enabled = data?.reboot_enabled === true;
	const active = data?.active;
	const last = data?.history?.[0];
	const isActive = active && REBOOT_ACTIVE_STATUSES.includes(active.status);

	let title = "Reboot this host";
	if (!connected) title = "Agent is not connected";
	else if (data && data.reboot_enabled === null)
		title =
			"The agent has not reported whether reboots are enabled (it may be too old)";
	else if (data && !enabled) title = disabledHint;
	else if (isActive) title = REBOOT_STATUS_LABELS[active.status];

	return (
		<>
			<button
				type="button"
				onClick={() => setOpen(true)}
				disabled={!connected || !enabled || isActive}
				className="btn-outline flex items-center gap-2 text-sm whitespace-nowrap"
				title={title}
			>
				{isActive ? (
					<RotateCw className="h-4 w-4 animate-spin" />
				) : (
					<Power className="h-4 w-4" />
				)}
				<span className="hidden sm:inline">
					{isActive ? REBOOT_STATUS_LABELS[active.status] : "Reboot"}
				</span>
			</button>
			{!isActive &&
				last &&
				["failed", "rejected", "timed_out"].includes(last.status) && (
					<span
						className="text-xs text-danger-600 dark:text-danger-400"
						title={last.reason || ""}
					>
						Last reboot: {REBOOT_STATUS_LABELS[last.status]}
					</span>
				)}
			{open && (
				<ModalPortal>
					<div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-[120] p-4">
						<div className="bg-white dark:bg-secondary-800 rounded-lg p-6 w-full max-w-md">
							<div className="flex items-center gap-3 mb-4">
								<div className="w-10 h-10 bg-warning-100 dark:bg-warning-900 rounded-full flex items-center justify-center">
									<Power className="h-5 w-5 text-warning-600 dark:text-warning-400" />
								</div>
								<div>
									<h3 className="text-lg font-semibold text-secondary-900 dark:text-white">
										Reboot host
									</h3>
									<p className="text-sm text-secondary-600 dark:text-secondary-300">
										{host.friendly_name}
									</p>
								</div>
							</div>

							{host.needs_reboot && host.reboot_reason && (
								<p className="mb-4 text-sm text-secondary-700 dark:text-secondary-200">
									Reason: {host.reboot_reason}
								</p>
							)}

							<fieldset className="mb-4">
								<legend className="block text-sm font-medium text-secondary-700 dark:text-secondary-200 mb-2">
									When
								</legend>
								<div className="flex gap-2 flex-wrap">
									{DELAYS.map((d) => (
										<button
											key={d}
											type="button"
											onClick={() => setDelay(d)}
											className={`px-3 py-1.5 text-sm rounded border ${
												delay === d
													? "bg-primary-600 border-primary-600 text-white"
													: "border-secondary-300 dark:border-secondary-600 text-secondary-700 dark:text-secondary-200"
											}`}
										>
											{d === 0 ? "Now" : `In ${d} min`}
										</button>
									))}
								</div>
							</fieldset>

							<label className="block mb-6">
								<span className="block text-sm font-medium text-secondary-700 dark:text-secondary-200 mb-1">
									Message to logged-in users (optional)
								</span>
								<input
									type="text"
									value={message}
									maxLength={200}
									onChange={(e) => setMessage(e.target.value)}
									className="w-full px-3 py-2 text-sm border border-secondary-300 dark:border-secondary-600 rounded bg-white dark:bg-secondary-700 text-secondary-900 dark:text-white"
									placeholder="Rebooting for kernel update"
								/>
							</label>

							<div className="flex justify-end gap-3">
								<button
									type="button"
									onClick={() => setOpen(false)}
									className="btn-outline"
									disabled={mutation.isPending}
								>
									Cancel
								</button>
								<button
									type="button"
									onClick={() => mutation.mutate()}
									className="btn-danger"
									disabled={mutation.isPending}
								>
									{mutation.isPending ? "Requesting…" : "Reboot"}
								</button>
							</div>
						</div>
					</div>
				</ModalPortal>
			)}
		</>
	);
};

export default RebootHostButton;
