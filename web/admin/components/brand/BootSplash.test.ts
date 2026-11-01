import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { I18nProvider } from "../../lib/i18n";

import { BootSplash } from "./BootSplash";

const render = (locale: "en" | "pt-BR") => renderToStaticMarkup(createElement(I18nProvider, { locale }, createElement(BootSplash)));
const html = render("pt-BR");

describe("BootSplash", () => {
  it("renders a labelled progressbar", () => {
    expect(html).toMatch(/role="progressbar"[^>]*aria-label="[^"]+"|aria-label="[^"]+"[^>]*role="progressbar"/);
  });

  it("shows the mcpgw title", () => {
    expect(html).toContain(">mcpgw</p>");
  });

  it("shows the PT-BR connecting line", () => {
    expect(html).toContain("Conectando servidores…");
  });

  it("shows the English copy", () => {
    const en = render("en");
    expect(en).toContain("Connecting servers…");
    expect(en).toContain("One MCP address for your agents and the servers behind it.");
  });
});
