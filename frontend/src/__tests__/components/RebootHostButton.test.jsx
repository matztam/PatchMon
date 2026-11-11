// fork: host reboot
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import RebootHostButton from "../../components/host/RebootHostButton";
import { rebootAPI } from "../../utils/rebootApi";

vi.mock("../../contexts/ToastContext", () => ({
	useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}));

vi.mock("../../utils/rebootApi", async (importOriginal) => {
	const actual = await importOriginal();
	return {
		...actual,
		rebootAPI: { getHostReboot: vi.fn(), requestReboot: vi.fn() },
	};
});

const host = {
	id: "h1",
	friendly_name: "web",
	needs_reboot: true,
	reboot_reason: "Kernel update",
};

function renderButton(props = {}) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<RebootHostButton host={host} connected {...props} />
		</QueryClientProvider>,
	);
}

describe("RebootHostButton", () => {
	beforeEach(() => vi.clearAllMocks());

	it("is disabled when the host has not enabled reboots", async () => {
		rebootAPI.getHostReboot.mockResolvedValue({
			reboot_enabled: false,
			active: null,
			history: [],
		});
		renderButton();
		const button = await screen.findByRole("button", { name: /reboot/i });
		await waitFor(() =>
			expect(button).toHaveAttribute(
				"title",
				expect.stringContaining("reboot-enabled"),
			),
		);
		expect(button).toBeDisabled();
	});

	it("is disabled while the agent is offline", async () => {
		rebootAPI.getHostReboot.mockResolvedValue({
			reboot_enabled: true,
			active: null,
			history: [],
		});
		renderButton({ connected: false });
		expect(
			await screen.findByRole("button", { name: /reboot/i }),
		).toBeDisabled();
	});

	it("requests a reboot with the chosen delay and message", async () => {
		rebootAPI.getHostReboot.mockResolvedValue({
			reboot_enabled: true,
			active: null,
			history: [],
		});
		rebootAPI.requestReboot.mockResolvedValue({ success: true });
		renderButton();
		const open = await screen.findByRole("button", { name: /reboot/i });
		await waitFor(() => expect(open).toBeEnabled());
		fireEvent.click(open);

		expect(screen.getByText("Reason: Kernel update")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "In 5 min" }));
		fireEvent.change(screen.getByPlaceholderText(/kernel update/i), {
			target: { value: "bye" },
		});
		const confirm = screen
			.getAllByRole("button", { name: "Reboot" })
			.find((b) => b.className.includes("btn-danger"));
		fireEvent.click(confirm);

		await waitFor(() =>
			expect(rebootAPI.requestReboot).toHaveBeenCalledWith("h1", {
				delayMinutes: 5,
				message: "bye",
			}),
		);
	});

	it("shows the status of a reboot in progress", async () => {
		rebootAPI.getHostReboot.mockResolvedValue({
			reboot_enabled: true,
			active: { id: "r1", status: "rebooting" },
			history: [{ id: "r1", status: "rebooting" }],
		});
		renderButton();
		expect(await screen.findByText("Rebooting…")).toBeInTheDocument();
	});
});
