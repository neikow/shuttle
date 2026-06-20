import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "./test/render";
import { App } from "./App";
import { api } from "./api";
import { setToken } from "./auth";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    api: {
      whoami: vi.fn(),
      overview: vi.fn(),
    },
  };
});

const mockApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
  setToken("test-bearer"); // App starts authed when a token is stored
  mockApi.overview.mockResolvedValue({ generated_at: "now", hosts: [] });
});

describe("App role gating", () => {
  it("hides admin tabs and mutation actions for a read token", async () => {
    mockApi.whoami.mockResolvedValue({ name: "reader", role: "read" });
    renderWithProviders(<App />);

    // Wait for the default Overview tab to settle.
    expect(await screen.findByRole("tab", { name: /Overview/ })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: /Tokens/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: /Webhooks/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Prune volumes" })).not.toBeInTheDocument();
  });

  it("shows admin tabs and mutation actions for an admin token", async () => {
    mockApi.whoami.mockResolvedValue({ name: "", role: "admin" });
    renderWithProviders(<App />);

    expect(await screen.findByRole("tab", { name: /Tokens/ })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Webhooks/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Prune volumes" })).toBeInTheDocument();
  });
});
