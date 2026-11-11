// fork: automatic reboot settings inside the patch policy form.
// The parent calls ref.current.save(policyId) after creating or updating the
// policy; the settings live in their own table and endpoint.
import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { rebootAPI, WEEKDAYS } from "../../utils/rebootApi";

const DEFAULTS = {
	reboot_mode: "never",
	window_days: [0, 1, 2, 3, 4, 5, 6],
	window_start: "02:00",
	window_end: "05:00",
	delay_minutes: 5,
	max_concurrent: 1,
	paused: false,
};

const inputClass =
	"w-full rounded border border-secondary-300 dark:border-secondary-600 bg-white dark:bg-secondary-700 text-secondary-900 dark:text-white px-3 py-2";
const labelClass =
	"block text-sm font-medium text-secondary-700 dark:text-secondary-300 mb-1";

const PolicyRebootSettings = forwardRef(function PolicyRebootSettings(
	{ policyId, orgTimezone },
	ref,
) {
	const [settings, setSettings] = useState(DEFAULTS);
	const [loaded, setLoaded] = useState(!policyId);
	const [loadError, setLoadError] = useState(null);

	useEffect(() => {
		let cancelled = false;
		if (!policyId) {
			setSettings(DEFAULTS);
			setLoaded(true);
			return undefined;
		}
		setLoaded(false);
		rebootAPI
			.getPolicyReboot(policyId)
			.then((data) => {
				if (!cancelled) {
					setSettings({ ...DEFAULTS, ...data });
					setLoaded(true);
				}
			})
			.catch((err) => {
				if (!cancelled) {
					setLoadError(err.response?.data?.error || err.message);
					setLoaded(true);
				}
			});
		return () => {
			cancelled = true;
		};
	}, [policyId]);

	useImperativeHandle(
		ref,
		() => ({
			save: (id) => {
				// Nothing to store for a new policy that keeps the default.
				if (!policyId && settings.reboot_mode === "never") {
					return Promise.resolve();
				}
				return rebootAPI.savePolicyReboot(id, {
					...settings,
					delay_minutes: Number(settings.delay_minutes),
					max_concurrent: Number(settings.max_concurrent),
				});
			},
		}),
		[policyId, settings],
	);

	const set = (patch) => setSettings((s) => ({ ...s, ...patch }));
	const toggleDay = (day) =>
		set({
			window_days: settings.window_days.includes(day)
				? settings.window_days.filter((d) => d !== day)
				: [...settings.window_days, day],
		});

	if (!loaded) {
		return (
			<p className="text-sm text-secondary-500 dark:text-secondary-400">
				Loading reboot settings…
			</p>
		);
	}

	const auto = settings.reboot_mode === "if_required";

	return (
		<fieldset className="space-y-3 border-t border-secondary-200 dark:border-secondary-700 pt-4">
			<legend className="text-sm font-semibold text-secondary-900 dark:text-white">
				Automatic reboot
			</legend>
			{loadError && (
				<p className="text-xs text-danger-600 dark:text-danger-400">
					Could not load reboot settings: {loadError}
				</p>
			)}
			<div>
				<label className={labelClass} htmlFor="policy-reboot-mode">
					Reboot hosts
				</label>
				<select
					id="policy-reboot-mode"
					value={settings.reboot_mode}
					onChange={(e) => set({ reboot_mode: e.target.value })}
					className={inputClass}
				>
					<option value="never">Never</option>
					<option value="if_required">
						When a reboot is required, inside the window
					</option>
				</select>
				<p className="mt-1 text-xs text-secondary-500 dark:text-secondary-400">
					Only hosts whose agent has <code>reboot-enabled: true</code> in its
					config.yml are rebooted. Hosts with a failed patch run since their
					last boot are skipped.
				</p>
			</div>
			{auto && (
				<>
					<div>
						<span className={labelClass}>Days</span>
						<div className="flex flex-wrap gap-1">
							{WEEKDAYS.map((d) => (
								<button
									key={d.value}
									type="button"
									onClick={() => toggleDay(d.value)}
									className={`px-2.5 py-1 text-xs rounded border ${
										settings.window_days.includes(d.value)
											? "bg-primary-600 border-primary-600 text-white"
											: "border-secondary-300 dark:border-secondary-600 text-secondary-700 dark:text-secondary-200"
									}`}
								>
									{d.label}
								</button>
							))}
						</div>
					</div>
					<div className="grid grid-cols-2 gap-3">
						<div>
							<label className={labelClass} htmlFor="policy-reboot-start">
								Window start
							</label>
							<input
								id="policy-reboot-start"
								type="time"
								value={settings.window_start}
								onChange={(e) => set({ window_start: e.target.value })}
								className={inputClass}
								required
							/>
						</div>
						<div>
							<label className={labelClass} htmlFor="policy-reboot-end">
								Window end
							</label>
							<input
								id="policy-reboot-end"
								type="time"
								value={settings.window_end}
								onChange={(e) => set({ window_end: e.target.value })}
								className={inputClass}
								required
							/>
						</div>
					</div>
					<p className="text-xs text-secondary-500 dark:text-secondary-400">
						Times in {orgTimezone}. An end before the start runs past midnight;
						equal times mean the whole day.
					</p>
					<div className="grid grid-cols-2 gap-3">
						<div>
							<label className={labelClass} htmlFor="policy-reboot-delay">
								Warning delay (minutes)
							</label>
							<input
								id="policy-reboot-delay"
								type="number"
								min={0}
								max={60}
								value={settings.delay_minutes}
								onChange={(e) => set({ delay_minutes: e.target.value })}
								className={inputClass}
							/>
						</div>
						<div>
							<label className={labelClass} htmlFor="policy-reboot-concurrent">
								Max. at the same time
							</label>
							<input
								id="policy-reboot-concurrent"
								type="number"
								min={1}
								max={100}
								value={settings.max_concurrent}
								onChange={(e) => set({ max_concurrent: e.target.value })}
								className={inputClass}
							/>
						</div>
					</div>
					<label className="flex items-center gap-2 text-sm text-secondary-700 dark:text-secondary-300">
						<input
							type="checkbox"
							checked={settings.paused}
							onChange={(e) => set({ paused: e.target.checked })}
						/>
						Paused (no automatic reboots for now)
					</label>
				</>
			)}
		</fieldset>
	);
});

export default PolicyRebootSettings;
