// These tests check the scheduling math in schedule.ts using the same
// cases the Go backend tests use (testdata/schedule_vectors.json), so both
// implementations are proven to agree on the same examples. The file is
// read directly off disk (rather than imported as a JSON module) so this
// test has no special TypeScript module-resolution requirements for a file
// that lives outside this project's own source tree.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
  now,
  nextLoopBoundary,
  nextCycleBoundary,
  nextVersionEffectiveAt,
  applySync,
  type WindowConfig,
  type Version,
  type ActiveSync,
} from "./schedule";

interface VectorItem {
  media_id: string;
  duration_ms: number;
}

interface VectorVersion {
  id: number;
  effective_at_ms: number;
  items: VectorItem[];
}

interface ScheduleCase {
  name: string;
  cycle_anchor_ms: number;
  cycle_ms: number;
  versions: VectorVersion[];
  t_ms: number;
  expected: {
    ok: boolean;
    item_index?: number;
    media_id?: string;
    offset_ms?: number;
    item_ends_at_ms?: number;
    cycle_ends_at_ms?: number;
  };
}

interface VectorSync {
  media_id: string;
  starts_at_ms: number;
  duration_ms: number;
  media_duration_ms: number;
}

interface SyncCase {
  name: string;
  sync: VectorSync | null;
  t_ms: number;
  expected: {
    active: boolean;
    media_id?: string;
    offset_ms?: number;
  };
}

interface VectorFile {
  schedule_cases: ScheduleCase[];
  sync_cases: SyncCase[];
}

const here = path.dirname(fileURLToPath(import.meta.url));
const vectorsPath = path.join(here, "..", "..", "..", "testdata", "schedule_vectors.json");
const vectors: VectorFile = JSON.parse(readFileSync(vectorsPath, "utf-8"));

function toWindowAndVersions(c: ScheduleCase): { window: WindowConfig; versions: Version[] } {
  return {
    window: { id: "W-test", cycleAnchorMs: c.cycle_anchor_ms },
    versions: c.versions.map((v) => ({
      id: v.id,
      effectiveAtMs: v.effective_at_ms,
      items: v.items.map((it) => ({ mediaId: it.media_id, durationMs: it.duration_ms })),
    })),
  };
}

describe("scheduler vectors (shared with the Go backend)", () => {
  for (const c of vectors.schedule_cases) {
    it(c.name, () => {
      const { window, versions } = toWindowAndVersions(c);
      const got = now(window, versions, c.cycle_ms, c.t_ms);

      expect(got.ok).toBe(c.expected.ok);
      if (!c.expected.ok) return;

      expect(got.itemIndex).toBe(c.expected.item_index);
      expect(got.mediaId).toBe(c.expected.media_id);
      expect(got.offsetMs).toBe(c.expected.offset_ms);
      expect(got.itemEndsAtMs).toBe(c.expected.item_ends_at_ms);
      expect(got.cycleEndsAtMs).toBe(c.expected.cycle_ends_at_ms);
    });
  }
});

describe("sync vectors (shared with the Go backend)", () => {
  for (const c of vectors.sync_cases) {
    it(c.name, () => {
      const sync: ActiveSync | null = c.sync
        ? {
            mediaId: c.sync.media_id,
            startsAtMs: c.sync.starts_at_ms,
            durationMs: c.sync.duration_ms,
            mediaDurationMs: c.sync.media_duration_ms,
          }
        : null;

      const got = applySync(sync, c.t_ms);
      expect(got.active).toBe(c.expected.active);
      if (!c.expected.active) return;

      expect(got.mediaId).toBe(c.expected.media_id);
      expect(got.offsetMs).toBe(c.expected.offset_ms);
    });
  }
});

describe("nextLoopBoundary", () => {
  const cases: Array<[string, number, number, number, number]> = [
    ["before loop starts", 10000, 5000, 3000, 10000],
    ["exactly on loop start", 10000, 5000, 10000, 10000],
    ["mid loop, one boundary ahead", 0, 60000, 15000, 60000],
    ["exactly on a later boundary", 0, 60000, 120000, 120000],
    ["just past a boundary", 0, 60000, 120001, 180000],
  ];
  for (const [name, loopStart, loopLen, t, want] of cases) {
    it(name, () => {
      expect(nextLoopBoundary(loopStart, loopLen, t)).toBe(want);
    });
  }
});

describe("nextCycleBoundary", () => {
  const cases: Array<[string, number, number, number, number]> = [
    ["start of first cycle", 0, 18000000, 0, 18000000],
    ["mid first cycle", 0, 18000000, 9000000, 18000000],
    ["exactly on a boundary", 0, 18000000, 36000000, 54000000],
    ["before anchor", 5000, 18000000, 1000, 5000],
  ];
  for (const [name, anchor, cycleMs, t, want] of cases) {
    it(name, () => {
      expect(nextCycleBoundary(anchor, cycleMs, t)).toBe(want);
    });
  }
});

describe("nextVersionEffectiveAt", () => {
  it("two adds in a row both end up in the final version's timing", () => {
    const window: WindowConfig = { id: "W1", cycleAnchorMs: 0 };
    const cycleMs = 18000000; // 5 hours
    const v1: Version = { id: 1, effectiveAtMs: 0, items: [{ mediaId: "M1", durationMs: 10000 }] };

    const now_ = 5000;
    const firstEffectiveAt = nextVersionEffectiveAt(window, [v1], cycleMs, now_);
    expect(firstEffectiveAt).toBe(10000);

    const v2: Version = {
      id: 2,
      effectiveAtMs: firstEffectiveAt,
      items: [
        { mediaId: "M1", durationMs: 10000 },
        { mediaId: "M2", durationMs: 20000 },
      ],
    };
    const secondEffectiveAt = nextVersionEffectiveAt(window, [v1, v2], cycleMs, now_);
    expect(secondEffectiveAt).toBe(10000);
  });

  it("empty playlist applies immediately", () => {
    const window: WindowConfig = { id: "W1", cycleAnchorMs: 0 };
    expect(nextVersionEffectiveAt(window, [], 18000000, 42000)).toBe(42000);
  });

  it("cycle boundary wins over a later loop boundary", () => {
    const window: WindowConfig = { id: "W1", cycleAnchorMs: 0 };
    const cycleMs = 20000;
    const v1: Version = { id: 1, effectiveAtMs: 0, items: [{ mediaId: "M1", durationMs: 15000 }] };
    expect(nextVersionEffectiveAt(window, [v1], cycleMs, 18000)).toBe(20000);
  });
});
