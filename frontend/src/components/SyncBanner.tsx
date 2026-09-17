// SyncBanner shows a visible countdown whenever a sync-playback event is
// upcoming or in progress, so anyone looking at the page understands why
// every window suddenly switched to showing the same thing. It renders
// nothing at all when there is no sync — most of the time, that's the
// case.
import type { SyncEvent } from "../api/client";

export interface SyncBannerProps {
  sync: SyncEvent | null;
  serverNowMs: number;
}

function secondsCeil(ms: number): number {
  return Math.max(0, Math.ceil(ms / 1000));
}

export function SyncBanner({ sync, serverNowMs }: SyncBannerProps) {
  if (!sync) return null;

  const startsAtMs = Date.parse(sync.starts_at);
  const endsAtMs = startsAtMs + sync.duration_seconds * 1000;

  // The sync record we were given has already fully played out (this can
  // briefly happen between polls) — nothing to announce.
  if (serverNowMs >= endsAtMs) return null;

  const mediaLabel = sync.media?.name ?? sync.media_id;
  const upcoming = serverNowMs < startsAtMs;

  return (
    <div className={`sync-banner ${upcoming ? "sync-banner--upcoming" : "sync-banner--active"}`}>
      {upcoming ? (
        <span>
          Sync starting in {secondsCeil(startsAtMs - serverNowMs)}s — every window will show{" "}
          <strong>{mediaLabel}</strong>
        </span>
      ) : (
        <span>
          Sync active: every window is showing <strong>{mediaLabel}</strong> — {secondsCeil(endsAtMs - serverNowMs)}s
          remaining
        </span>
      )}
    </div>
  );
}
