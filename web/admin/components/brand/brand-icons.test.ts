import { describe, expect, it } from "vitest";
import { siLaravel } from "simple-icons";

import { getBrandIcon, hasBrandIcon } from "./brand-icons";

/** Every server registered in the gateway must resolve to a real brand mark, never the monogram. */
const REGISTERED_SERVERS: ReadonlyArray<readonly [slug: string, url: string | undefined]> = [
  ["cloudflare", "https://mcp.cloudflare.com/mcp"],
  ["crawl4ai", undefined],
  ["dokploy", undefined],
  ["firecrawl", undefined],
  ["forgeboard", undefined],
  ["github", "https://api.githubcopilot.com/mcp/readonly"],
  ["obscura", undefined],
  ["portainer", undefined],
  ["rea", undefined],
  ["ui_layouts", undefined],
  ["vgpu", "https://vgpu.sh/api/mcp"],
  ["redis", undefined],
  ["postgres", undefined],
  ["chrome_devtools", undefined],
  ["kafka", undefined],
  ["telescope", "https://dev.example.com/telescope-mcp"],
  ["easypanel", undefined],
  ["vercel", "https://mcp.vercel.com"],
  ["netlify", "https://netlify-mcp.netlify.app/mcp"],
  ["meta_social_technologies", "https://mcp.facebook.com/devtools"],
  ["whatsapp_business_tools", "https://mcp.facebook.com/whatsapp_business_tools"],
];
// "acme" is deliberately absent: it is a placeholder slug with no real project or brand behind it.

describe("registered servers have a brand icon", () => {
  it.each(REGISTERED_SERVERS)("%s", (slug, url) => {
    expect(hasBrandIcon(slug, url)).toBe(true);
  });

  it("resolves telescope by slug, not by its host", () => {
    const icon = getBrandIcon("telescope", "https://dev.example.com/telescope-mcp");
    expect(icon?.paths[0].d).toBe(siLaravel.path);
  });

  it.each(["codex", "vscode"])("agent %s has a brand icon", (slug) => {
    expect(hasBrandIcon(slug)).toBe(true);
  });

  it("keeps the monogram for unknown slugs", () => {
    expect(hasBrandIcon("acme", "http://127.0.0.1:9190/")).toBe(false);
  });
});
