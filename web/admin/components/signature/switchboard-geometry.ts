// Ported from design/v5/src/components/signature/switchboard-geometry.ts on 2026-10-31.
/* Pure geometry for the Switchboard wires. Everything is in px, relative to the diagram box. */

export interface Point {
  x: number;
  y: number;
}

export interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
  cx: number;
  cy: number;
}

export interface SwitchboardGeometry {
  width: number;
  height: number;
  vertical: boolean;
  /** Agent id -> wire into the core. */
  agentWires: Record<string, string>;
  coreToKey: string;
  /** Static wiring on the server side: key to bus, the bus, and one stub into each tile row. */
  serverWires: string[];
  /** Server slug -> route from the key out to the tile. Hidden slugs use overflowRoute. */
  tileRoutes: Record<string, string>;
  overflowRoute: string;
  core: Point;
  keyIn: Point;
  orbit: string;
}

const CORNER = 10;
const round = (n: number) => Math.round(n * 10) / 10;

export function toBox(rect: DOMRect, origin: DOMRect): Box {
  const left = rect.left - origin.left;
  const top = rect.top - origin.top;
  return { left, top, right: left + rect.width, bottom: top + rect.height, cx: left + rect.width / 2, cy: top + rect.height / 2 };
}

/** Polyline with rounded corners (quadratic) through the given points. */
export function roundedPath(points: Point[], radius = CORNER): string {
  const pts = points.filter((p, i) => i === 0 || Math.hypot(p.x - points[i - 1].x, p.y - points[i - 1].y) > 0.5);
  if (pts.length < 2) return "";
  let d = `M${round(pts[0].x)} ${round(pts[0].y)}`;
  for (let i = 1; i < pts.length - 1; i++) {
    const [prev, cur, next] = [pts[i - 1], pts[i], pts[i + 1]];
    const inLen = Math.hypot(cur.x - prev.x, cur.y - prev.y);
    const outLen = Math.hypot(next.x - cur.x, next.y - cur.y);
    const r = Math.min(radius, inLen / 2, outLen / 2);
    const a = { x: cur.x - ((cur.x - prev.x) / inLen) * r, y: cur.y - ((cur.y - prev.y) / inLen) * r };
    const b = { x: cur.x + ((next.x - cur.x) / outLen) * r, y: cur.y + ((next.y - cur.y) / outLen) * r };
    d += ` L${round(a.x)} ${round(a.y)} Q${round(cur.x)} ${round(cur.y)} ${round(b.x)} ${round(b.y)}`;
  }
  const last = pts[pts.length - 1];
  return `${d} L${round(last.x)} ${round(last.y)}`;
}

/** Concatenate path segments that end where the next one starts into one continuous path. */
export function joinPaths(...segments: string[]): string {
  return segments
    .filter(Boolean)
    .map((d, i) => (i === 0 ? d : d.replace(/^M/, "L")))
    .join(" ");
}

/** A large circle passing behind the core, bulging right like the art's orbit; only an arc is drawn. */
function orbitArc(core: Point, height: number): string {
  const r = height * 1.15;
  const cx = core.x + r * 0.98;
  const cy = core.y + r * 0.2;
  const theta = Math.atan2(core.y - cy, core.x - cx);
  const spread = 0.95;
  const from = { x: cx + r * Math.cos(theta - spread), y: cy + r * Math.sin(theta - spread) };
  const to = { x: cx + r * Math.cos(theta + spread), y: cy + r * Math.sin(theta + spread) };
  return `M${round(from.x)} ${round(from.y)} A${round(r)} ${round(r)} 0 0 1 ${round(to.x)} ${round(to.y)}`;
}

export interface RowLayoutInput {
  width: number;
  height: number;
  agents: Record<string, Box>;
  core: Box;
  key: Box;
  /** Visible tiles in DOM order. */
  tiles: { slug: string; box: Box }[];
  grid: Box;
  rowGap: number;
}

/** Desktop: agents fan into the core on a bus, key feeds a bus that runs rails between tile rows. */
export function rowGeometry(input: RowLayoutInput): SwitchboardGeometry {
  const { core, key, tiles, grid, rowGap } = input;
  const agentBox = Object.values(input.agents);
  const agentsRight = agentBox.length ? Math.max(...agentBox.map((b) => b.right)) : core.left - 40;
  const agentBus = (agentsRight + core.left) / 2;
  const coreIn = { x: core.left, y: core.cy };
  const keyIn = { x: key.left, y: key.cy };
  const keyOut = { x: key.right, y: key.cy };
  const serverBus = Math.max(key.right + 16, grid.left - 18);

  const agentWires = Object.fromEntries(
    Object.entries(input.agents).map(([id, box]) => [
      id,
      roundedPath([{ x: box.right, y: box.cy }, { x: agentBus, y: box.cy }, { x: agentBus, y: core.cy }, coreIn]),
    ]),
  );

  const rowTops = [...new Set(tiles.map((t) => Math.round(t.box.top)))].sort((a, b) => a - b);
  const gutters = rowTops.map((top) => top - rowGap / 2);
  const gutterOf = (box: Box) => gutters[rowTops.indexOf(Math.round(box.top))] ?? gutters[0] ?? key.cy;

  const tileRoutes = Object.fromEntries(
    tiles.map(({ slug, box }) => {
      const gutter = gutterOf(box);
      return [slug, roundedPath([keyOut, { x: serverBus, y: keyOut.y }, { x: serverBus, y: gutter }, { x: box.cx, y: gutter }, { x: box.cx, y: box.top }], 8)];
    }),
  );

  // Like the art: one bus left of the grid, a short stub into the first tile of every row.
  const rowCenters = rowTops.map((top) => tiles.find((t) => Math.round(t.box.top) === top)?.box.cy ?? top);
  const lastGutter = gutters[gutters.length - 1] ?? keyOut.y;
  const busTop = Math.min(gutters[0] ?? keyOut.y, keyOut.y);
  const busBottom = Math.max(rowCenters[rowCenters.length - 1] ?? keyOut.y, keyOut.y);
  const serverWires = [
    roundedPath([keyOut, { x: serverBus, y: keyOut.y }]),
    `M${round(serverBus)} ${round(busTop)} V${round(busBottom)}`,
    ...rowCenters.map((y) => `M${round(serverBus)} ${round(y)} H${round(grid.left - 3)}`),
  ];

  return {
    width: input.width,
    height: input.height,
    vertical: false,
    agentWires,
    coreToKey: roundedPath([{ x: core.right, y: core.cy }, keyIn]),
    serverWires,
    tileRoutes,
    overflowRoute: roundedPath([keyOut, { x: serverBus, y: keyOut.y }, { x: serverBus, y: lastGutter }, { x: grid.right + 24, y: lastGutter }], 8),
    core: { x: core.cx, y: core.cy },
    keyIn,
    orbit: orbitArc({ x: core.cx, y: core.cy }, input.height),
  };
}

export interface StackLayoutInput {
  width: number;
  height: number;
  agentIds: string[];
  agentsRow: Box;
  core: Box;
  key: Box;
  keyColumn: Box;
  serversRow: Box;
}

/** Mobile: one vertical spine, agents row to core to key to servers row. */
export function stackGeometry(input: StackLayoutInput): SwitchboardGeometry {
  const { agentsRow, core, key, keyColumn, serversRow } = input;
  const x = core.cx;
  const intoCore = roundedPath([{ x, y: agentsRow.bottom + 4 }, { x, y: core.top }]);
  const toServers = roundedPath([{ x, y: keyColumn.bottom + 4 }, { x, y: serversRow.top - 4 }]);
  return {
    width: input.width,
    height: input.height,
    vertical: true,
    agentWires: Object.fromEntries(input.agentIds.map((id) => [id, intoCore])),
    coreToKey: roundedPath([{ x, y: core.bottom }, { x, y: key.top }]),
    serverWires: [toServers],
    tileRoutes: {},
    overflowRoute: toServers,
    core: { x: core.cx, y: core.cy },
    keyIn: { x, y: key.top },
    orbit: orbitArc({ x: core.cx, y: core.cy }, Math.max(input.height * 0.45, 260)),
  };
}
