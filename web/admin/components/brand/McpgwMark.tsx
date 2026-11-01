import { cn } from "../../lib/utils";

export interface McpgwMarkProps {
  className?: string;
  /** Paint the gateway dot with the core token. Off keeps the whole mark one color (marks on colored backgrounds). */
  core?: boolean;
  /** Optical variant for renders at 16 px or less: heavier stroke, brackets pulled apart, bigger dot. */
  small?: boolean;
}

/** The mcpgw symbol: two ports facing each other with the gateway dot between them. Brackets inherit currentColor. */
export function McpgwMark({ className, core = false, small = false }: McpgwMarkProps) {
  return (
    <svg viewBox="0 0 64 64" fill="none" className={cn("size-5", className)} aria-hidden>
      <g stroke="currentColor" strokeWidth={small ? 8 : 6} strokeLinecap="round" strokeLinejoin="round">
        <path d={small ? "M22 12H10v40h12" : "M24 12H12v40h12"} />
        <path d={small ? "M42 12h12v40H42" : "M40 12h12v40H40"} />
      </g>
      <circle cx="32" cy="32" r={small ? 7 : 6} className={core ? "fill-core" : "fill-current"} />
    </svg>
  );
}

export interface McpgwTileProps extends McpgwMarkProps {
  /** Sizes the tile (e.g. `size-12`). */
  className?: string;
  /** Sizes the mark inside the tile. */
  markClassName?: string;
}

/** The gateway tile: solid tile, hairline border, spark brackets and the core dot. No gradient, no glow. */
export function McpgwTile({ className, markClassName, small }: McpgwTileProps) {
  return (
    <span className={cn("grid shrink-0 place-items-center rounded-[22%] border border-line-strong bg-tile text-spark", className)}>
      <McpgwMark core small={small} className={markClassName} />
    </span>
  );
}
