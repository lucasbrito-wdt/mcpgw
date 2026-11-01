import { beforeAll, describe, expect, it } from "vitest";

import { setCurrentLocale } from "../../lib/i18n";

import { callsPerMinute, formatLatency, formatPerMinute, kindLabel, LOGS_WINDOWS, statusMeta } from "./logs-format";

describe("logs windows", () => {
  it("offers 5m, 15m, 1h and 24h only", () => {
    expect(LOGS_WINDOWS).toEqual(["5m", "15m", "1h", "24h"]);
  });

  it("divides calls by the window minutes", () => {
    expect(callsPerMinute(30, "15m")).toBe(2);
    expect(callsPerMinute(1440, "24h")).toBe(1);
  });
});

describe("logs format in pt-BR", () => {
  beforeAll(() => setCurrentLocale("pt-BR"));

  it("uses the decimal comma", () => {
    expect(formatPerMinute(callsPerMinute(1, "5m"))).toBe("0,2");
    expect(formatLatency(1500)).toBe("1,5 s");
  });

  it("labels statuses and kinds in Portuguese", () => {
    expect(statusMeta("failed").label).toBe("Falhou");
    expect(kindLabel("search")).toBe("Busca");
    expect(statusMeta("custom").label).toBe("custom");
  });
});

describe("logs format in en", () => {
  beforeAll(() => setCurrentLocale("en"));

  it("uses the decimal point", () => {
    expect(formatPerMinute(callsPerMinute(1, "5m"))).toBe("0.2");
    expect(formatLatency(1500)).toBe("1.5 s");
  });

  it("labels statuses and kinds in English", () => {
    expect(statusMeta("timed_out").label).toBe("Timed out");
    expect(kindLabel("execute")).toBe("Execution");
  });
});
