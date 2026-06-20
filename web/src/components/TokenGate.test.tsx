import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TokenGate } from "./TokenGate";
import * as oidc from "../oidc";
import { verifyToken } from "../api";

vi.mock("../oidc", () => ({
  getAuthConfig: vi.fn(),
  startOidcLogin: vi.fn(),
}));
vi.mock("../api", () => ({ verifyToken: vi.fn() }));

const mockOidc = vi.mocked(oidc);
const mockVerify = vi.mocked(verifyToken);

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe("TokenGate", () => {
  it("offers SSO when the orchestrator advertises OIDC, and starts the flow", async () => {
    mockOidc.getAuthConfig.mockResolvedValue({
      oidc_enabled: true,
      issuer: "https://idp.example",
      client_id: "shuttle",
    });
    mockOidc.startOidcLogin.mockResolvedValue(undefined);
    const user = userEvent.setup();
    render(<TokenGate onAuthed={() => {}} />);

    const sso = await screen.findByRole("button", { name: "Sign in with SSO" });
    await user.click(sso);
    expect(mockOidc.startOidcLogin).toHaveBeenCalledOnce();
  });

  it("hides SSO when OIDC is not configured", async () => {
    mockOidc.getAuthConfig.mockResolvedValue({ oidc_enabled: false });
    render(<TokenGate onAuthed={() => {}} />);
    // Give the effect a tick; the SSO button must never appear.
    await waitFor(() => expect(screen.getByPlaceholderText("bearer token")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Sign in with SSO" })).not.toBeInTheDocument();
  });

  it("still accepts a pasted bearer token", async () => {
    mockOidc.getAuthConfig.mockResolvedValue({ oidc_enabled: false });
    mockVerify.mockResolvedValue(true);
    const onAuthed = vi.fn();
    const user = userEvent.setup();
    render(<TokenGate onAuthed={onAuthed} />);

    await user.type(screen.getByPlaceholderText("bearer token"), "static-bearer");
    await user.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() => expect(onAuthed).toHaveBeenCalledOnce());
    expect(mockVerify).toHaveBeenCalledOnce();
  });

  it("surfaces an initial SSO error passed from the callback handler", async () => {
    mockOidc.getAuthConfig.mockResolvedValue({ oidc_enabled: true, issuer: "x", client_id: "y" });
    render(<TokenGate onAuthed={() => {}} initialError="SSO login failed." />);
    expect(await screen.findByText("SSO login failed.")).toBeInTheDocument();
  });
});
