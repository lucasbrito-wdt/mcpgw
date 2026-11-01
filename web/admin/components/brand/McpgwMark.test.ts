import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { McpgwMark, McpgwTile } from "./McpgwMark";

const mark = (props: Parameters<typeof McpgwMark>[0] = {}) => renderToStaticMarkup(createElement(McpgwMark, props));
const tile = (props: Parameters<typeof McpgwTile>[0] = {}) => renderToStaticMarkup(createElement(McpgwTile, props));

describe("McpgwMark", () => {
  it("uses the standard geometry by default", () => {
    const html = mark();
    expect(html).toContain('d="M24 12H12v40h12"');
    expect(html).toContain('d="M40 12h12v40H40"');
    expect(html).toContain('stroke-width="6"');
    expect(html).toContain('r="6"');
  });

  it("uses the optical small geometry when small", () => {
    const html = mark({ small: true });
    expect(html).toContain('d="M22 12H10v40h12"');
    expect(html).toContain('d="M42 12h12v40H42"');
    expect(html).toContain('stroke-width="8"');
    expect(html).toContain('r="7"');
    expect(html).not.toContain("M24 12H12v40h12");
  });

  it("paints the dot with currentColor by default", () => {
    const html = mark();
    expect(html).toContain('class="fill-current"');
    expect(html).not.toContain("fill-core");
  });

  it("paints the dot with the core token when core", () => {
    const html = mark({ core: true });
    expect(html).toContain('class="fill-core"');
    expect(html).not.toContain("fill-current");
  });

  it("is hidden from assistive tech", () => {
    expect(mark()).toContain('aria-hidden="true"');
  });

  it("merges a custom className onto the svg", () => {
    expect(mark({ className: "size-8" })).toMatch(/<svg[^>]*class="[^"]*size-8/);
  });
});

describe("McpgwTile", () => {
  it("has no gradient classes", () => {
    expect(tile()).not.toMatch(/bg-linear|from-|to-/);
  });

  it("has no shadow or glow classes", () => {
    expect(tile()).not.toMatch(/shadow|glow/);
  });

  it("renders the core-colored mark inside", () => {
    expect(tile()).toContain('class="fill-core"');
  });

  it("forwards small to the mark", () => {
    expect(tile({ small: true })).toContain('d="M22 12H10v40h12"');
  });

  it("applies markClassName to the mark and className to the tile", () => {
    const html = tile({ className: "size-12", markClassName: "size-6" });
    expect(html).toMatch(/^<span[^>]*class="[^"]*size-12/);
    expect(html).toMatch(/<svg[^>]*class="[^"]*size-6/);
  });
});
