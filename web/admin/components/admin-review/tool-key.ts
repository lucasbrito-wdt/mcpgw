import type { Tool } from "../../lib/admin-types";

/** Identity of a tool in the review queue: server plus name (names are unique per server). */
export const toolKey = (tool: Pick<Tool, "server" | "name">): string => `${tool.server}/${tool.name}`;
