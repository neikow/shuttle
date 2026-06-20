import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "../test/render";
import { Deploys } from "./Deploys";
import { api } from "../api";

vi.mock("../api", () => ({
  api: {
    deploys: vi.fn(),
    deployLogs: vi.fn(),
    deploy: vi.fn(),
    rollback: vi.fn(),
  },
}));

const mockApi = vi.mocked(api);

const oneDeploy = [
  {
    DeployID: "d-1",
    Service: "web",
    Host: "web1",
    SHA: "abcdef0123456789",
    Status: "success",
    TriggeredBy: "webhook",
    WebhookNonce: "",
    StartedAt: "2026-06-20T10:00:00Z",
  },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockApi.deploys.mockResolvedValue(oneDeploy);
});

describe("Deploys logs", () => {
  it("opens a deploy's logs in a modal, coloring stderr", async () => {
    mockApi.deployLogs.mockResolvedValue([
      { at: "2026-06-20T10:00:01Z", stream: "stdout", text: "Pulling image" },
      { at: "2026-06-20T10:00:02Z", stream: "stderr", text: "boom: failed to start" },
    ]);
    const user = userEvent.setup();
    renderWithProviders(<Deploys />, "read");

    await user.click(await screen.findByRole("button", { name: "Logs" }));

    expect(mockApi.deployLogs).toHaveBeenCalledWith("d-1");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Pulling image")).toBeInTheDocument();
    expect(within(dialog).getByText("boom: failed to start")).toBeInTheDocument();
  });

  it("shows an empty-state when a deploy has no captured logs", async () => {
    mockApi.deployLogs.mockResolvedValue([]);
    const user = userEvent.setup();
    renderWithProviders(<Deploys />, "read");

    await user.click(await screen.findByRole("button", { name: "Logs" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/No output captured/i)).toBeInTheDocument();
  });

  it("offers Logs to the read role but not Redeploy/Rollback", async () => {
    renderWithProviders(<Deploys />, "read");
    expect(await screen.findByRole("button", { name: "Logs" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Redeploy" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Rollback" })).not.toBeInTheDocument();
  });
});
