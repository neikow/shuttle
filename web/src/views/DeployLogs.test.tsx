import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "../test/render";
import { LogsModal } from "./DeployLogs";
import { api } from "../api";
import type { ShuttleEvent } from "../types";

vi.mock("../api", () => ({ api: { deployLogs: vi.fn() } }));
vi.mock("../auth", () => ({ getToken: () => "t" }));

// Capture the fetchEventSource handlers so a test can push events through them.
let onmessage: ((m: { data: string }) => void) | undefined;
vi.mock("@microsoft/fetch-event-source", () => ({
  fetchEventSource: vi.fn(async (_url: string, opts: any) => {
    await opts.onopen?.();
    onmessage = opts.onmessage;
  }),
}));

const mockApi = vi.mocked(api);

const runningDeploy = {
  DeployID: "d-9",
  Service: "web",
  Host: "web1",
  SHA: "abcdef0123456789",
  Status: "running",
  TriggeredBy: "manual",
  WebhookNonce: "",
  StartedAt: "2026-06-22T10:00:00Z",
};

function emit(ev: Partial<ShuttleEvent>) {
  onmessage?.({ data: JSON.stringify(ev) });
}

beforeEach(() => {
  vi.clearAllMocks();
  onmessage = undefined;
  mockApi.deployLogs.mockResolvedValue([]); // not yet persisted while running
});

describe("LogsModal live tail", () => {
  it("appends deploy.log lines for the matching deploy", async () => {
    renderWithProviders(<LogsModal deploy={runningDeploy} onClose={() => {}} />);

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/streaming/i)).toBeInTheDocument();

    emit({ type: "deploy.log", deploy_id: "d-9", message: "pulling\nstarting" });
    emit({ type: "deploy.log", deploy_id: "other", message: "ignore me" });

    expect(await within(dialog).findByText("pulling")).toBeInTheDocument();
    expect(within(dialog).getByText("starting")).toBeInTheDocument();
    expect(within(dialog).queryByText("ignore me")).not.toBeInTheDocument();
  });

  it("does not stream for a terminal deploy", async () => {
    const { fetchEventSource } = await import("@microsoft/fetch-event-source");
    renderWithProviders(
      <LogsModal deploy={{ ...runningDeploy, Status: "success" }} onClose={() => {}} />,
    );
    await screen.findByRole("dialog");
    expect(fetchEventSource).not.toHaveBeenCalled();
  });
});
