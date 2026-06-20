import { UserManager, WebStorageStateStore } from "oidc-client-ts";
import type { AuthConfig } from "./types";
import { api } from "./api";

// Browser OIDC login (Authorization Code + PKCE) layered on the existing
// bearer-token auth: the orchestrator already accepts an OIDC ID token as a
// bearer (it verifies aud == client_id and maps the roles claim to a role), so
// the SPA just needs to obtain that ID token and hand it to the normal bearer
// storage. PKCE, discovery, state/nonce, and the code exchange are delegated to
// oidc-client-ts (a maintained, security-reviewed library) rather than
// hand-rolled — consistent with the server using coreos/go-oidc for the same
// reason.

let cachedConfig: AuthConfig | null = null;
let manager: UserManager | null = null;

export async function getAuthConfig(): Promise<AuthConfig> {
  if (!cachedConfig) cachedConfig = await api.authConfig();
  return cachedConfig;
}

// The SPA is served under Vite's base path (/ui/); return there after login.
function redirectUri(): string {
  return window.location.origin + import.meta.env.BASE_URL;
}

async function getManager(): Promise<UserManager | null> {
  const cfg = await getAuthConfig();
  if (!cfg.oidc_enabled || !cfg.issuer || !cfg.client_id) return null;
  if (!manager) {
    manager = new UserManager({
      authority: cfg.issuer,
      client_id: cfg.client_id,
      redirect_uri: redirectUri(),
      response_type: "code", // triggers PKCE; public client, no secret
      scope: cfg.scopes || "openid profile email",
      // Keep the in-flight login state in sessionStorage; we don't persist the
      // oidc-client user (the ID token is mirrored into our own bearer storage).
      userStore: new WebStorageStateStore({ store: window.sessionStorage }),
      stateStore: new WebStorageStateStore({ store: window.sessionStorage }),
    });
  }
  return manager;
}

// oidcAvailable reports whether the orchestrator advertises a usable OIDC config.
export async function oidcAvailable(): Promise<boolean> {
  return !!(await getManager());
}

// startOidcLogin redirects the browser to the IdP to begin the code flow.
export async function startOidcLogin(): Promise<void> {
  const m = await getManager();
  if (!m) throw new Error("OIDC is not configured");
  await m.signinRedirect();
}

// completeOidcLogin finishes the flow when this page load is the IdP redirect
// back (URL carries ?code & ?state). It exchanges the code for tokens, strips the
// auth params from the URL, and returns the ID token to use as the bearer — or
// null when this is not a callback.
export async function completeOidcLogin(): Promise<string | null> {
  const params = new URLSearchParams(window.location.search);
  if (!params.has("code") || !params.has("state")) return null;
  const m = await getManager();
  if (!m) return null;
  try {
    const user = await m.signinRedirectCallback();
    return user.id_token ?? null;
  } finally {
    window.history.replaceState({}, document.title, redirectUri());
  }
}

// oidcLogout clears any oidc-client session state (best-effort).
export async function oidcLogout(): Promise<void> {
  if (manager) {
    await manager.removeUser().catch(() => undefined);
  }
}
