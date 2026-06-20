import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "../test/render";
import { Webhooks } from "./Webhooks";
import { api } from "../api";

vi.mock("../api", () => ({
  api: {
    listWebhooks: vi.fn(),
    createWebhook: vi.fn(),
    deleteWebhook: vi.fn(),
  },
}));

const mockApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
  mockApi.listWebhooks.mockResolvedValue([]);
});

describe("Webhooks view", () => {
  it("creates a webhook and shows its trigger URL", async () => {
    mockApi.createWebhook.mockResolvedValue({ id: "wh-abc" });
    const user = userEvent.setup();
    renderWithProviders(<Webhooks />);

    await user.type(screen.getByLabelText("service name"), "web");
    await user.click(screen.getByRole("button", { name: "Create" }));

    expect(mockApi.createWebhook).toHaveBeenCalledWith("web");
    expect(await screen.findByText(/\/webhook\/repo\/wh-abc$/)).toBeInTheDocument();
  });

  it("deletes a webhook after confirmation", async () => {
    mockApi.listWebhooks.mockResolvedValue([
      { ID: "wh-1", Service: "web", CreatedAt: "2026-01-01T00:00:00Z" },
    ]);
    mockApi.deleteWebhook.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithProviders(<Webhooks />);

    await user.click(await screen.findByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(mockApi.deleteWebhook).toHaveBeenCalledWith("wh-1"));
  });
});
