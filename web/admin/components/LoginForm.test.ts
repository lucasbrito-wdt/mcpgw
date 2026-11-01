import { describe, expect, it } from "vitest";

import { resolveNextTarget } from "./login-target";

const ORIGIN = "https://gw.example.com";
const next = (value: string | null) => resolveNextTarget(value, "admin", ORIGIN);

describe("resolveNextTarget", () => {
  it("keeps a same-area path", () => {
    expect(next("/admin/servers/")).toBe("/admin/servers/");
    expect(next("/admin/servers/?tab=1#x")).toBe("/admin/servers/?tab=1#x");
  });

  it.each(["//evil.com", "/\\evil.com", "/\t/evil.com", "/%09/evil.com", "https://evil.com", "https://evil.com/admin/", "/portal/", "/admin", "javascript:alert(1)", "", "/admin/login/?next=%2Fadmin%2F"])(
    "falls back to the area home for %j",
    (value) => {
      expect(next(value)).toBe("/admin/");
    },
  );

  it("falls back when next is missing", () => {
    expect(next(null)).toBe("/admin/");
  });
});
