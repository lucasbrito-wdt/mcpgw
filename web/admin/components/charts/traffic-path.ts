// Pure geometry for TrafficChart: turns zero-filled stats buckets into SVG path data.

export interface ChartSize {
  width: number;
  height: number;
}

export interface Point {
  x: number;
  callsY: number;
  deniedY: number;
}

export interface TrafficGeometry {
  /** Value at the top of the y axis. */
  max: number;
  calls: string;
  callsArea: string;
  denied: string;
  points: Point[];
}

/** Smallest 1, 2 or 5 times a power of ten that is >= value (at least 1), so the y axis ends on a round number. */
export function niceMax(value: number): number {
  if (value <= 1) return 1;
  const base = 10 ** Math.floor(Math.log10(value));
  return [1, 2, 5, 10].map((step) => step * base).find((candidate) => candidate >= value) ?? 10 * base;
}

const round = (n: number) => Math.round(n * 100) / 100;

const line = (points: readonly Point[], key: "callsY" | "deniedY") =>
  points.map((p, i) => `${i === 0 ? "M" : "L"}${p.x} ${p[key]}`).join("");

export function buildTrafficPaths(series: readonly { calls: number; denied: number }[], { width, height }: ChartSize): TrafficGeometry {
  const max = niceMax(Math.max(0, ...series.map((b) => b.calls)));
  const y = (value: number) => round(height - (value / max) * height);
  const points = series.map<Point>((bucket, i) => ({
    x: series.length === 1 ? width / 2 : round((i / (series.length - 1)) * width),
    callsY: y(bucket.calls),
    deniedY: y(bucket.denied),
  }));
  const first = points[0];
  const last = points[points.length - 1];
  return {
    max,
    calls: line(points, "callsY"),
    callsArea: first ? `M${first.x} ${height}${line(points, "callsY").replace("M", "L")}L${last.x} ${height}Z` : "",
    denied: line(points, "deniedY"),
    points,
  };
}

/** Index of the busiest bucket (first on ties); -1 when there is no traffic. */
export function peakBucket(series: readonly { calls: number }[]): number {
  let best = -1;
  let bestCalls = 0;
  series.forEach((bucket, i) => {
    if (bucket.calls > bestCalls) {
      best = i;
      bestCalls = bucket.calls;
    }
  });
  return best;
}
