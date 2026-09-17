// This file answers the exact same question as the backend's
// backend/internal/schedule/schedule.go: "for this window, what should be
// playing right now?" It is a line-for-line port of that Go code into
// TypeScript, on purpose — every browser computes what to show completely
// on its own, from the playlist data and the current time, without ever
// asking the server "what's next?". For that to work, this file and the
// Go file must always agree on the answer. testdata/schedule_vectors.json
// holds shared example cases that both schedule.test.ts (here) and
// schedule_test.go (on the backend) run, specifically to catch the two
// implementations ever drifting apart.
//
// All timestamps here are plain numbers: milliseconds since the Unix
// epoch, the same unit JavaScript's Date.now() and Date.parse() use.

export interface Item {
  mediaId: string;
  durationMs: number;
}

// One saved copy of a window's playlist, and the moment it takes effect.
// See the Go file's comment on why versions exist instead of editing a
// playlist in place.
export interface Version {
  id: number;
  effectiveAtMs: number;
  items: Item[];
}

export interface WindowConfig {
  id: string;
  cycleAnchorMs: number;
}

// What a window should be showing at one instant. When ok is false, there
// is nothing to play (empty playlist, or the window's first cycle hasn't
// started yet) and the caller should show a fallback state rather than
// treat it as an error — the other fields are meaningless in that case.
export interface PlaybackState {
  ok: boolean;
  versionId: number;
  itemIndex: number;
  mediaId: string;
  durationMs: number;
  offsetMs: number;
  itemEndsAtMs: number;
  cycleEndsAtMs: number;
  loopStartMs: number;
  loopLenMs: number;
}

const NOT_PLAYING: PlaybackState = {
  ok: false,
  versionId: 0,
  itemIndex: 0,
  mediaId: "",
  durationMs: 0,
  offsetMs: 0,
  itemEndsAtMs: 0,
  cycleEndsAtMs: 0,
  loopStartMs: 0,
  loopLenMs: 0,
};

// floorDiv/floorMod round toward negative infinity, unlike JavaScript's
// built-in "%" (which, like Go's, keeps the sign of the left-hand side).
// That matters whenever a timestamp can land before the point it's being
// measured from. IEEE 754 division of two integers that are exactly
// representable (as all our millisecond values are, being far below
// 2^53) is correctly rounded, so Math.floor/Math.ceil on the plain
// division give exact integer results here — no separate sign-correction
// trick is needed the way Go's integer division requires.
function floorDiv(a: number, b: number): number {
  return Math.floor(a / b);
}

function floorMod(a: number, b: number): number {
  return a - floorDiv(a, b) * b;
}

function ceilDiv(a: number, b: number): number {
  return Math.ceil(a / b);
}

function selectVersion(versions: Version[], tMs: number): Version | null {
  let best: Version | null = null;
  for (const v of versions) {
    if (v.effectiveAtMs > tMs) continue;
    if (
      !best ||
      v.effectiveAtMs > best.effectiveAtMs ||
      (v.effectiveAtMs === best.effectiveAtMs && v.id > best.id)
    ) {
      best = v;
    }
  }
  return best;
}

// now computes what a window should be showing at time T.
//
// Worked example: a playlist is 10s + 20s + 30s (60s total, one "loop").
// 75 seconds have passed since the loop started. 75 mod 60 = 15, so we are
// 15 seconds into this pass of the loop. Walking the items: the first item
// covers 0-10s, the second covers 10-30s. 15s falls in the second item,
// 5 seconds into it ("15 - 10 = 5"). So the answer is: item 2 ("M2"),
// 5 seconds in.
export function now(window: WindowConfig, versions: Version[], cycleMs: number, tMs: number): PlaybackState {
  if (tMs < window.cycleAnchorMs) {
    return NOT_PLAYING;
  }

  const v = selectVersion(versions, tMs);
  if (!v || v.items.length === 0) {
    return NOT_PLAYING;
  }

  const elapsedSinceAnchor = tMs - window.cycleAnchorMs;
  const cycleStart = window.cycleAnchorMs + floorDiv(elapsedSinceAnchor, cycleMs) * cycleMs;
  const cycleEndsAt = cycleStart + cycleMs;

  const loopStart = Math.max(cycleStart, v.effectiveAtMs);

  let loopLen = 0;
  for (const item of v.items) loopLen += item.durationMs;
  if (loopLen <= 0) return NOT_PLAYING;

  const pos = floorMod(tMs - loopStart, loopLen);

  let acc = 0;
  for (let i = 0; i < v.items.length; i++) {
    const item = v.items[i];
    if (pos < acc + item.durationMs) {
      const offset = pos - acc;
      const remaining = item.durationMs - offset;
      let itemEndsAt = tMs + remaining;
      if (itemEndsAt > cycleEndsAt) itemEndsAt = cycleEndsAt;
      return {
        ok: true,
        versionId: v.id,
        itemIndex: i,
        mediaId: item.mediaId,
        durationMs: item.durationMs,
        offsetMs: offset,
        itemEndsAtMs: itemEndsAt,
        cycleEndsAtMs: cycleEndsAt,
        loopStartMs: loopStart,
        loopLenMs: loopLen,
      };
    }
    acc += item.durationMs;
  }

  // Unreachable: pos is always < loopLen (floorMod guarantees it) and the
  // items' durations sum to loopLen, so the loop above always returns.
  return NOT_PLAYING;
}

// nextLoopBoundary finds the next timestamp at or after T when the given
// loop restarts from its first item. If T already lands exactly on a
// boundary, that same timestamp is returned.
export function nextLoopBoundary(loopStartMs: number, loopLenMs: number, tMs: number): number {
  if (loopLenMs <= 0) return tMs;
  const delta = tMs - loopStartMs;
  if (delta <= 0) return loopStartMs;
  const n = ceilDiv(delta, loopLenMs);
  return loopStartMs + n * loopLenMs;
}

// nextCycleBoundary finds the next timestamp at or after T when the
// window's fixed-length cycle restarts from item 1.
export function nextCycleBoundary(cycleAnchorMs: number, cycleMs: number, tMs: number): number {
  const elapsed = tMs - cycleAnchorMs;
  if (elapsed < 0) return cycleAnchorMs;
  const cycleStart = cycleAnchorMs + floorDiv(elapsed, cycleMs) * cycleMs;
  return cycleStart + cycleMs;
}

// nextVersionEffectiveAt decides when a newly-created playlist version
// should take over: never before the currently-playing loop or cycle would
// restart anyway, and never before a change that's already been promised.
// See the matching Go function's comment for the full rationale — this is
// only used on the frontend to preview "applies at ..." before the backend
// confirms it.
export function nextVersionEffectiveAt(window: WindowConfig, versions: Version[], cycleMs: number, tMs: number): number {
  const state = now(window, versions, cycleMs, tMs);

  let candidate: number;
  if (!state.ok) {
    candidate = tMs;
  } else {
    const loopBoundary = nextLoopBoundary(state.loopStartMs, state.loopLenMs, tMs);
    const cycleBoundary = nextCycleBoundary(window.cycleAnchorMs, cycleMs, tMs);
    candidate = Math.min(loopBoundary, cycleBoundary);
  }

  let latestKnown: number | null = null;
  for (const v of versions) {
    if (latestKnown === null || v.effectiveAtMs > latestKnown) {
      latestKnown = v.effectiveAtMs;
    }
  }
  if (latestKnown !== null && latestKnown > candidate) {
    candidate = latestKnown;
  }
  return candidate;
}

// Describes a sync-playback event: "show this one media item on every
// window at once". mediaDurationMs is the actual length of that media
// item; if the sync's own durationMs runs longer than the media, the media
// simply loops for the rest of the sync — that's what keeps every
// window's video at the exact same playback position.
export interface ActiveSync {
  mediaId: string;
  startsAtMs: number;
  durationMs: number;
  mediaDurationMs: number;
}

export interface SyncState {
  active: boolean;
  mediaId: string;
  offsetMs: number;
}

const SYNC_NOT_ACTIVE: SyncState = { active: false, mediaId: "", offsetMs: 0 };

// applySync decides whether a sync event is currently controlling playback
// at time T, and if so, how far into the (possibly looping) media it is.
//
// Worked example: sync media M2 is 15s long, sync duration is 40s, and
// sync started 32 seconds ago. 32 mod 15 = 2, so every window shows M2
// exactly 2 seconds into its playback (M2 has already looped twice).
export function applySync(sync: ActiveSync | null, tMs: number): SyncState {
  if (!sync) return SYNC_NOT_ACTIVE;
  if (tMs < sync.startsAtMs || tMs >= sync.startsAtMs + sync.durationMs) {
    return SYNC_NOT_ACTIVE;
  }
  const elapsed = tMs - sync.startsAtMs;
  const offset = sync.mediaDurationMs > 0 ? floorMod(elapsed, sync.mediaDurationMs) : elapsed;
  return { active: true, mediaId: sync.mediaId, offsetMs: offset };
}
