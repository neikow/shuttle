import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "../test/render";
import { Tokens } from "./Tokens";
import { api } from "../api";

vi.mock("../api", () => ({
  api: {
    listTokens: vi.fn(),
    createToken: vi.fn(),
    revokeToken: vi.fn(),
  },
}));

const mockApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
  mockApi.listTokens.mockResolvedValue([]);
});

describe("Tokens view", () => {
  it("mints a token and shows the plaintext once in a modal", async () => {
    mockApi.createToken.mockResolvedValue({
      id: "1",
      name: "ci",
      role: "deploy",
      token: "plaintext-secret-123",
    });
    const user = userEvent.setup();
    renderWithProviders(<Tokens />);

    await user.type(screen.getByLabelText("token name"), "ci");
    await user.selectOptions(screen.getByLabelText("token role"), "deploy");
    await user.click(screen.getByRole("button", { name: "Mint" }));

    expect(mockApi.createToken).toHaveBeenCalledWith("ci", "deploy");
    expect(await screen.findByText("plaintext-secret-123")).toBeInTheDocument();
  });

  it("revokes a token after confirmation", async () => {
    mockApi.listTokens.mockResolvedValue([
      { id: "tok-1", name: "old", role: "read", created_at: "2026-01-01T00:00:00Z" },
    ]);
    mockApi.revokeToken.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithProviders(<Tokens />);

    await user.click(await screen.findByRole("button", { name: "Revoke" }));
    // Confirm dialog opens; the confirm button is labelled "Revoke" too — pick
    // the one inside the dialog.
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(mockApi.revokeToken).toHaveBeenCalledWith("tok-1"));
  });
});
