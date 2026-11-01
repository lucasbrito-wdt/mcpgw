import { useT } from "../../lib/i18n";
import { boot } from "../../lib/i18n/messages/boot";

import { McpgwTile } from "./McpgwMark";

const AGENTS = ["Claude Code", "Codex", "Cursor", "VS Code"];
const SERVERS = ["GitHub", "Slack", "Linear", "Notion"];

/** Route geometry in the SVG's own units; strokes keep their screen width (non-scaling-stroke). */
const ROW_PITCH = 44; // chip row height (32) plus gap (12) at 1 unit = 1 px
const rowY = (index: number) => 16 + ROW_PITCH * index; // center of chip `index`
const MIDDLE = 16 + ROW_PITCH * 1.5; // center of the four rows, where the tile sits

function horizontalPath(index: number, direction: "in" | "out"): string {
  const y = rowY(index);
  return direction === "in" ? `M0 ${y} C50 ${y} 50 ${MIDDLE} 100 ${MIDDLE}` : `M0 ${MIDDLE} C50 ${MIDDLE} 50 ${y} 100 ${y}`;
}

function verticalPath(column: 0 | 1, direction: "in" | "out"): string {
  const x = column === 0 ? 25 : 75;
  return direction === "in" ? `M${x} 0 C${x} 20 50 20 50 40` : `M50 0 C50 20 ${x} 20 ${x} 40`;
}

function Routes({ paths, viewBox, className }: { paths: string[]; viewBox: string; className: string }) {
  return (
    <svg viewBox={viewBox} preserveAspectRatio="none" fill="none" className={className} aria-hidden>
      {paths.map((d) => (
        <path
          key={d}
          d={d}
          stroke="var(--color-signal-fg)"
          strokeOpacity={0.55}
          strokeWidth={1.5}
          strokeDasharray="6 10"
          vectorEffect="non-scaling-stroke"
          className="animate-route-flow motion-reduce:animate-none"
        />
      ))}
    </svg>
  );
}

function Chips({ names, className }: { names: string[]; className?: string }) {
  return (
    <ul className={className}>
      {names.map((name) => (
        <li key={name} className="flex h-8 items-center justify-center rounded-full border border-line bg-tile/60 px-3 text-xs font-medium whitespace-nowrap text-fg">
          {name}
        </li>
      ))}
    </ul>
  );
}

const HORIZONTAL_ROUTES = "hidden h-41 w-24 shrink-0 md:block lg:w-32";

/**
 * Full-screen first-load state: agents on one side, servers on the other, the gateway tile between them
 * and dashed routes flowing through it. Agents on top and servers below under md.
 */
export function BootSplash() {
  const t = useT(boot);
  return (
    <div className="fixed inset-0 z-50 flex flex-col items-center overflow-y-auto bg-void px-6 py-10">
      <div className="flex w-full flex-1 flex-col items-center justify-center gap-8">
        <div aria-hidden className="flex w-full flex-col items-center md:w-auto md:flex-row">
          <Chips names={AGENTS} className="grid w-full max-w-xs grid-cols-2 gap-3 md:flex md:w-auto md:max-w-none md:flex-col" />
          <Routes
            paths={[0, 1, 2, 3].map((i) => horizontalPath(i, "in"))}
            viewBox="0 0 100 164"
            className={HORIZONTAL_ROUTES}
          />
          <Routes
            paths={([0, 1] as const).map((c) => verticalPath(c, "in"))}
            viewBox="0 0 100 40"
            className="h-10 w-full max-w-xs md:hidden"
          />
          <McpgwTile className="size-20" markClassName="size-9" />
          <Routes
            paths={([0, 1] as const).map((c) => verticalPath(c, "out"))}
            viewBox="0 0 100 40"
            className="h-10 w-full max-w-xs md:hidden"
          />
          <Routes
            paths={[0, 1, 2, 3].map((i) => horizontalPath(i, "out"))}
            viewBox="0 0 100 164"
            className={HORIZONTAL_ROUTES}
          />
          <Chips names={SERVERS} className="grid w-full max-w-xs grid-cols-2 gap-3 md:flex md:w-auto md:max-w-none md:flex-col" />
        </div>

        <div className="flex flex-col items-center gap-2 text-center">
          <p className="font-brand text-2xl font-semibold tracking-[-0.04em] text-fg">mcpgw</p>
          <p className="max-w-sm text-sm text-fg-muted">{t("tagline")}</p>
        </div>
      </div>

      <div className="flex w-full max-w-60 flex-col items-center gap-3">
        <div role="progressbar" aria-label={t("progressLabel")} className="h-0.75 w-full overflow-hidden rounded-full bg-line">
          <div className="h-full w-[30%] rounded-full bg-core animate-progress-sweep motion-reduce:w-full motion-reduce:animate-none motion-reduce:opacity-40" />
        </div>
        <p className="text-xs text-fg-muted">{t("connecting")}</p>
      </div>
    </div>
  );
}
