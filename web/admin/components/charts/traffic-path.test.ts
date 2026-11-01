import { describe, expect, it } from "vitest";

import { buildTrafficPaths, niceMax, peakBucket } from "./traffic-path";

const SIZE = { width: 100, height: 40 };

describe("niceMax", () => {
  it("rounds up to 1, 2, 5 times a power of ten and never below 1", () => {
    expect([0, 1, 3, 7, 12, 130, 4100].map(niceMax)).toEqual([1, 1, 5, 10, 20, 200, 5000]);
  });
});

describe("buildTrafficPaths", () => {
  it("returns empty paths for an empty series", () => {
    const g = buildTrafficPaths([], SIZE);
    expect(g).toMatchObject({ calls: "", callsArea: "", denied: "", points: [] });
  });

  it("spreads buckets over the width and scales y by the nice max (y grows downward)", () => {
    const g = buildTrafficPaths(
      [
        { calls: 0, denied: 0 },
        { calls: 5, denied: 1 },
        { calls: 10, denied: 0 },
      ],
      SIZE,
    );
    expect(g.max).toBe(10);
    expect(g.points.map((p) => [p.x, p.callsY, p.deniedY])).toEqual([
      [0, 40, 40],
      [50, 20, 36],
      [100, 0, 40],
    ]);
    expect(g.calls).toBe("M0 40L50 20L100 0");
    expect(g.denied).toBe("M0 40L50 36L100 40");
    expect(g.callsArea).toBe("M0 40L0 40L50 20L100 0L100 40Z");
  });

  it("centers a single bucket", () => {
    const g = buildTrafficPaths([{ calls: 2, denied: 0 }], SIZE);
    expect(g.points[0].x).toBe(50);
    expect(g.calls).toBe("M50 0");
  });

  it("keeps an all-zero series on the baseline", () => {
    const g = buildTrafficPaths(
      [
        { calls: 0, denied: 0 },
        { calls: 0, denied: 0 },
      ],
      SIZE,
    );
    expect(g.max).toBe(1);
    expect(g.calls).toBe("M0 40L100 40");
  });
});

describe("peakBucket", () => {
  it("returns the index of the busiest bucket, the first on ties, or -1 when empty or idle", () => {
    expect(peakBucket([{ calls: 1 }, { calls: 4 }, { calls: 4 }])).toBe(1);
    expect(peakBucket([{ calls: 0 }])).toBe(-1);
    expect(peakBucket([])).toBe(-1);
  });
});
