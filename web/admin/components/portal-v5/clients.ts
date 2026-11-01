// Ported from design/v5/src/data/{agents,portal-agents}.ts on 2026-10-31.
// Static facts about MCP clients (how to install them); the address comes from window.location.origin.
import type { Translator } from "../../lib/i18n";
import type { portalAgents } from "../../lib/i18n/messages/portalAgents";
import type { Agent, AgentId } from "../../lib/types";

type T = Translator<typeof portalAgents.en>;

export const AGENTS: readonly Agent[] = [
  { id: "claude-code", name: "Claude Code" },
  { id: "cursor", name: "Cursor" },
  { id: "codex", name: "Codex" },
  { id: "claude-desktop", name: "Claude Desktop" },
  { id: "vscode", name: "VS Code" },
];

/** The four shown in the portal switchboard. */
export const SWITCHBOARD_AGENTS: readonly Agent[] = AGENTS.slice(0, 4);

export function agentName(id: AgentId): string {
  return AGENTS.find((agent) => agent.id === id)?.name ?? id;
}

export type InstallMethod = "url" | "oneclick" | "cli";

export const METHOD_LABEL_KEY = { url: "methodUrl", oneclick: "methodOneclick", cli: "methodCli" } as const;

export interface ClientSetup {
  id: AgentId;
  /** Where to paste the address in the app; absent when the client has no URL field. */
  paste?: string;
  oneClick?: { label: string; href: string };
  cli?: string;
  /** Config file the client reads, with its path and contents. */
  file: { path: string; language: "JSON" | "TOML"; body: string };
}

const json = (value: unknown) => JSON.stringify(value, null, 2);

/** Install instructions per client for one MCP address (`<origin>/mcp`). */
export function clientSetups(mcpUrl: string, t: T): Record<AgentId, ClientSetup> {
  const cursorConfig = json({ url: mcpUrl });
  const vscodeConfig = json({ name: "mcpgw", type: "http", url: mcpUrl });
  return {
    "claude-code": {
      id: "claude-code",
      cli: `claude mcp add --transport http mcpgw ${mcpUrl}`,
      file: { path: t("claudeCodeFile"), language: "JSON", body: json({ mcpServers: { mcpgw: { type: "http", url: mcpUrl } } }) },
    },
    cursor: {
      id: "cursor",
      paste: t("pasteCursor"),
      oneClick: { label: t("installInCursor"), href: `cursor://anysphere.cursor-deeplink/mcp/install?name=mcpgw&config=${btoa(cursorConfig)}` },
      file: { path: "~/.cursor/mcp.json", language: "JSON", body: json({ mcpServers: { mcpgw: { url: mcpUrl } } }) },
    },
    codex: {
      id: "codex",
      cli: `codex mcp add mcpgw --url ${mcpUrl}`,
      file: { path: "~/.codex/config.toml", language: "TOML", body: `[mcp_servers.mcpgw]\nurl = "${mcpUrl}"` },
    },
    "claude-desktop": {
      id: "claude-desktop",
      paste: t("pasteClaudeDesktop"),
      file: {
        path: "claude_desktop_config.json",
        language: "JSON",
        body: json({ mcpServers: { mcpgw: { command: "npx", args: ["-y", "mcp-remote", mcpUrl] } } }),
      },
    },
    vscode: {
      id: "vscode",
      paste: t("pasteVscode"),
      oneClick: { label: t("installInVscode"), href: `vscode:mcp/install?${encodeURIComponent(vscodeConfig)}` },
      cli: `code --add-mcp '${JSON.stringify({ name: "mcpgw", type: "http", url: mcpUrl })}'`,
      file: { path: ".vscode/mcp.json", language: "JSON", body: json({ servers: { mcpgw: { type: "http", url: mcpUrl } } }) },
    },
  };
}

/** Why a method is missing for a client, so the empty slot still says something useful. */
export function missingMethodNote(client: ClientSetup, method: InstallMethod, name: string, t: T): string {
  if (method === "oneclick") return t(client.cli ? "oneclickMissing_cli" : "oneclickMissing_url", { name });
  if (method === "cli") return t(client.oneClick ? "cliMissing_oneclick" : "cliMissing_url", { name });
  return t(client.cli ? "urlMissing_cli" : "urlMissing_file", { name });
}
