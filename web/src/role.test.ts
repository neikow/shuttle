import { describe, expect, it } from "vitest";
import { canAdmin, canDeploy, roleRank } from "./role";

describe("roleRank", () => {
  it("is strictly increasing read < deploy < admin", () => {
    expect(roleRank("read")).toBeLessThan(roleRank("deploy"));
    expect(roleRank("deploy")).toBeLessThan(roleRank("admin"));
  });
  it("ranks unknown roles 0", () => {
    expect(roleRank("bogus")).toBe(0);
  });
});

describe("canDeploy", () => {
  it("allows deploy and admin, denies read and unknown", () => {
    expect(canDeploy("read")).toBe(false);
    expect(canDeploy("deploy")).toBe(true);
    expect(canDeploy("admin")).toBe(true);
    expect(canDeploy("bogus")).toBe(false);
  });
});

describe("canAdmin", () => {
  it("allows only admin", () => {
    expect(canAdmin("read")).toBe(false);
    expect(canAdmin("deploy")).toBe(false);
    expect(canAdmin("admin")).toBe(true);
  });
});
