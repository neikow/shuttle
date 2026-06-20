import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";
import { setToken } from "./auth";

// Builds a minimal Response-like object for the api client's request() helper,
// which reads .ok, .status, .headers.get("Content-Type"), .json() and .text().
function makeRes(
  status: number,
  body: unknown = null,
  contentType = "application/json",
): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (k: string) => (k === "Content-Type" ? contentType : null) },
    json: async () => body,
    text: async () => (typeof body === "string" ? body : JSON.stringify(body)),
  } as unknown as Response;
}

const TOKEN = "test-bearer";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  setToken(TOKEN);
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// last returns the [url, init] of the most recent fetch call.
function last(): [string, RequestInit] {
  return fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit];
}

function authHeader(init: RequestInit): string | undefined {
  return (init.headers as Record<string, string>)?.["Authorization"];
}

describe("api mutations", () => {
  it("deploy POSTs to /deploy/{service}?sha= with the bearer", async () => {
    fetchMock.mockResolvedValue(makeRes(202, { deploy_id: "d1" }));
    await api.deploy("web", "abc123");
    const [url, init] = last();
    expect(url).toBe("/deploy/web?sha=abc123");
    expect(init.method).toBe("POST");
    expect(authHeader(init)).toBe(`Bearer ${TOKEN}`);
  });

  it("rollback POSTs to /rollback with service + steps", async () => {
    fetchMock.mockResolvedValue(makeRes(202, { deploy_id: "d1", target_sha: "x" }));
    await api.rollback("web", 3);
    const [url, init] = last();
    expect(url).toBe("/rollback?service=web&steps=3");
    expect(init.method).toBe("POST");
  });

  it("rollback defaults steps to 1", async () => {
    fetchMock.mockResolvedValue(makeRes(202, {}));
    await api.rollback("web");
    expect(last()[0]).toBe("/rollback?service=web&steps=1");
  });

  it("prune POSTs to /prune and returns the pruned list", async () => {
    fetchMock.mockResolvedValue(makeRes(200, { pruned: ["a", "b"] }));
    const res = await api.prune();
    expect(last()[0]).toBe("/prune");
    expect(res.pruned).toEqual(["a", "b"]);
  });

  it("createToken POSTs JSON body to /tokens", async () => {
    fetchMock.mockResolvedValue(makeRes(201, { id: "1", name: "n", role: "read", token: "tok" }));
    const res = await api.createToken("n", "read");
    const [url, init] = last();
    expect(url).toBe("/tokens");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ name: "n", role: "read" }));
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
    expect(res.token).toBe("tok");
  });

  it("revokeToken DELETEs /tokens/{id} (204, no body)", async () => {
    fetchMock.mockResolvedValue(makeRes(204));
    await api.revokeToken("id-1");
    const [url, init] = last();
    expect(url).toBe("/tokens/id-1");
    expect(init.method).toBe("DELETE");
  });

  it("createWebhook POSTs the service", async () => {
    fetchMock.mockResolvedValue(makeRes(201, { id: "wh1" }));
    const res = await api.createWebhook("web");
    const [url, init] = last();
    expect(url).toBe("/webhooks/repo");
    expect(init.body).toBe(JSON.stringify({ service: "web" }));
    expect(res.id).toBe("wh1");
  });

  it("deleteWebhook DELETEs /webhooks/repo/{id}", async () => {
    fetchMock.mockResolvedValue(makeRes(204));
    await api.deleteWebhook("wh1");
    const [url, init] = last();
    expect(url).toBe("/webhooks/repo/wh1");
    expect(init.method).toBe("DELETE");
  });

  it("enroll POSTs the host", async () => {
    fetchMock.mockResolvedValue(
      makeRes(201, { id: "j1", host: "web-1", join_token: "jt", expires_at_unix_ms: 0 }),
    );
    const res = await api.enroll("web-1");
    const [url, init] = last();
    expect(url).toBe("/enroll");
    expect(init.body).toBe(JSON.stringify({ host: "web-1" }));
    expect(res.join_token).toBe("jt");
  });

  it("throws ApiError with the status on a non-ok response", async () => {
    fetchMock.mockResolvedValue(makeRes(403, "forbidden", "text/plain"));
    await expect(api.prune()).rejects.toMatchObject({
      constructor: ApiError,
      status: 403,
    });
  });
});
