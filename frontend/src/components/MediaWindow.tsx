// MediaWindow is one "TV screen" on the page. It doesn't do any scheduling
// math itself — the parent (App.tsx) already worked out which media item
// should be showing and how far into it we are, using the shared scheduler
// in src/schedule. This component's only job is to render whatever it's
// told to render: an image, a video seeked to the right position, a blank
// placeholder, or a "nothing configured" fallback — and to show a small
// progress readout underneath.
import { useEffect, useRef, useState } from "react";
import type { Media } from "../api/client";

export interface MediaWindowProps {
  windowName: string;
  ok: boolean;
  media: Media | null;
  // The media that will play right after this one, if known. Rendered
  // off-screen so the browser has already fetched it by the time it's
  // actually needed, avoiding a loading gap at the switch.
  nextMedia?: Media | null;
  offsetMs: number;
  durationMs: number;
  itemEndsAtMs: number | null;
  cycleEndsAtMs: number | null;
  serverNowMs: number;
  synced: boolean;
}

function formatRemaining(ms: number): string {
  const totalSeconds = Math.max(0, Math.ceil(ms / 1000));
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  const pad = (n: number) => n.toString().padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

export function MediaWindow(props: MediaWindowProps) {
  const { windowName, ok, media, nextMedia, offsetMs, durationMs, itemEndsAtMs, cycleEndsAtMs, serverNowMs, synced } = props;

  const videoRef = useRef<HTMLVideoElement>(null);
  const [loadFailed, setLoadFailed] = useState(false);

  // A brand new media item means loadFailed (from whatever played before)
  // no longer applies.
  useEffect(() => {
    setLoadFailed(false);
  }, [media?.id]);

  // This effect runs on every tick (whenever offsetMs changes, which is
  // every ~250ms from the parent's clock), and does two jobs with one
  // piece of logic: when the item has just switched, the video starts at
  // currentTime 0 which is far from the expected offset, so it gets
  // seeked immediately; while the SAME item keeps playing, small natural
  // drift between the video's own playback clock and our computed offset
  // gets corrected once it passes 0.5 seconds, without visibly restarting
  // the video for tiny differences.
  useEffect(() => {
    const video = videoRef.current;
    if (!video || media?.type !== "video") return;
    const expectedSeconds = offsetMs / 1000;
    const drift = Math.abs(video.currentTime - expectedSeconds);
    if (drift > 0.5) {
      video.currentTime = expectedSeconds;
    }
  }, [offsetMs, media?.id, media?.type]);

  const itemRemainingMs = itemEndsAtMs !== null ? itemEndsAtMs - serverNowMs : null;
  const cycleRemainingMs = cycleEndsAtMs !== null ? cycleEndsAtMs - serverNowMs : null;
  const progressPct = ok && durationMs > 0 ? Math.min(100, Math.max(0, (offsetMs / durationMs) * 100)) : 0;

  return (
    <div className="media-window">
      <div className="media-window__header">
        <span className="media-window__name">{windowName}</span>
        {synced && <span className="media-window__badge">SYNCED</span>}
      </div>

      <div className="media-window__stage">
        {!ok && <div className="media-window__fallback">No media configured</div>}

        {ok && media?.type === "blank" && <div className="media-window__blank" />}

        {ok && media?.type === "image" && !loadFailed && (
          <img
            key={media.id}
            className="media-window__media"
            src={media.url ?? undefined}
            alt={media.name}
            onError={() => setLoadFailed(true)}
          />
        )}

        {ok && media?.type === "video" && !loadFailed && (
          <video
            key={media.id}
            ref={videoRef}
            className="media-window__media"
            src={media.url ?? undefined}
            muted
            playsInline
            autoPlay
            onError={() => setLoadFailed(true)}
          />
        )}

        {ok && loadFailed && <div className="media-window__fallback">Media failed to load</div>}

        {nextMedia?.type === "video" && (
          <video className="media-window__preload" src={nextMedia.url ?? undefined} muted preload="auto" />
        )}
      </div>

      <div className="media-window__footer">
        <div className="media-window__label">{ok ? media?.name ?? media?.id : "—"}</div>
        <div className="media-window__progress-track">
          <div className="media-window__progress-fill" style={{ width: `${progressPct}%` }} />
        </div>
        <div className="media-window__times">
          <span>{itemRemainingMs !== null ? `item: ${formatRemaining(itemRemainingMs)}` : ""}</span>
          <span>{cycleRemainingMs !== null ? `cycle: ${formatRemaining(cycleRemainingMs)}` : ""}</span>
        </div>
      </div>
    </div>
  );
}
