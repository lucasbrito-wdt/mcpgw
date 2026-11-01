import { describe, expect, it } from "vitest";

import { resolveBrandKey } from "./resolve-brand";

const KEYS = new Set(["github", "cloudflare", "brave-search"]);
const has = (key: string) => KEYS.has(key);

describe("resolveBrandKey", () => {
  it("matches the exact slug", () => {
    expect(resolveBrandKey("github", undefined, has)).toBe("github");
    expect(resolveBrandKey("brave-search", undefined, has)).toBe("brave-search");
  });

  it("falls back to the first segment of the slug", () => {
    expect(resolveBrandKey("cloudflare_acme", undefined, has)).toBe("cloudflare");
    expect(resolveBrandKey("GitHub-enterprise", undefined, has)).toBe("github");
  });

  it("falls back to a host label of the url", () => {
    expect(resolveBrandKey("edge", "https://mcp.cloudflare.com/sse", has)).toBe("cloudflare");
    expect(resolveBrandKey("edge", "api.github.com", has)).toBe("github");
  });

  it("prefers the slug over the url", () => {
    expect(resolveBrandKey("github_x", "https://mcp.cloudflare.com", has)).toBe("github");
  });

  it("returns undefined for unknown slugs and bad urls", () => {
    expect(resolveBrandKey("forgeboard", undefined, has)).toBeUndefined();
    expect(resolveBrandKey("rea", "http://[bad", has)).toBeUndefined();
  });
});
