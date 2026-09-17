// App is the top-level page. It owns the one thing every other piece of
// this frontend depends on: the app's current data (what's in each
// window's playlist, whether a sync is running) and the current time
// according to the server's clock. Everything else — MediaWindow,
// ControlPanel, SyncBanner — is handed already-computed values as props
// and doesn't talk to the backend or the scheduler itself.
//
// Three independent clocks run here, each on its own schedule:
//   1. Clock sync: once on load, then every 60s — keeps our estimate of
//      "what time does the server think it is" accurate.
//   2. State polling: every 2s, with an ETag so an unchanged answer costs
//      almost nothing — keeps playlists and sync events up to date.
//   3. A fast local tick, every 250ms — re-runs the scheduler against
//      whatever data we already have, so progress bars and countdowns
//      move smoothly and an item switch happens at the right instant even
//      between polls, without waiting on the network at all.
import { useCallback, useEffect, useRef, useState } from "react";
import {
  addPlaylistItem,
  cancelSync,
  createMedia,
  createSync,
  getState,
  listMedia,
  type Media,
  type MediaType,
  type PlaylistVersion,
  type StateResponse,
  type SyncEvent,
  type WindowState,
} from "./api/client";
import { ClockSync } from "./clock/offset";
import { applySync, now as scheduleNow, type ActiveSync, type Version, type WindowConfig } from "./schedule/schedule";
import { MediaWindow } from "./components/MediaWindow";
import { ControlPanel } from "./components/ControlPanel";
import { SyncBanner } from "./components/SyncBanner";

const BASE_URL: string = import.meta.env.VITE_API_BASE_URL ?? "http://localhost:8080";

function toScheduleVersion(v: PlaylistVersion): Version {
  return {
    id: v.id,
    effectiveAtMs: Date.parse(v.effective_at),
    items: v.items.map((it) => ({
      mediaId: it.media_id,
      durationMs: (it.media?.duration_seconds ?? 0) * 1000,
    })),
  };
}

interface WindowDisplay {
  ok: boolean;
  media: Media | null;
  nextMedia: Media | null;
  offsetMs: number;
  durationMs: number;
  itemEndsAtMs: number | null;
  cycleEndsAtMs: number | null;
  synced: boolean;
}

// computeWindowDisplay is where the API's data, the scheduler's math, and
// any active sync all come together into "here's exactly what this one
// window should show right now". It's called fresh on every tick (every
// 250ms) rather than only when new data arrives from the server, which is
// what keeps playback smooth between polls.
function computeWindowDisplay(win: WindowState, sync: SyncEvent | null, cycleSeconds: number, serverNowMs: number): WindowDisplay {
  const versions: Version[] = [];
  if (win.current_version) versions.push(toScheduleVersion(win.current_version));
  for (const v of win.upcoming_versions) versions.push(toScheduleVersion(v));

  const windowConfig: WindowConfig = { id: win.id, cycleAnchorMs: Date.parse(win.cycle_anchor) };
  const state = scheduleNow(windowConfig, versions, cycleSeconds * 1000, serverNowMs);

  let media: Media | null = null;
  let nextMedia: Media | null = null;
  if (state.ok) {
    const allVersions = win.current_version ? [win.current_version, ...win.upcoming_versions] : win.upcoming_versions;
    const activeVersion = allVersions.find((v) => v.id === state.versionId);
    if (activeVersion && activeVersion.items.length > 0) {
      media = activeVersion.items[state.itemIndex]?.media ?? null;
      const nextIndex = (state.itemIndex + 1) % activeVersion.items.length;
      nextMedia = activeVersion.items[nextIndex]?.media ?? null;
    }
  }

  let activeSync: ActiveSync | null = null;
  if (sync) {
    activeSync = {
      mediaId: sync.media_id,
      startsAtMs: Date.parse(sync.starts_at),
      durationMs: sync.duration_seconds * 1000,
      mediaDurationMs: (sync.media?.duration_seconds ?? 0) * 1000,
    };
  }
  const syncState = applySync(activeSync, serverNowMs);

  if (syncState.active && activeSync) {
    return {
      ok: true,
      media: sync?.media ?? null,
      nextMedia: null,
      offsetMs: syncState.offsetMs,
      durationMs: (sync?.media?.duration_seconds ?? 0) * 1000,
      // While synced, "time left in this item" means time left in the
      // sync itself — normal playback resumes exactly when it ends.
      itemEndsAtMs: activeSync.startsAtMs + activeSync.durationMs,
      cycleEndsAtMs: state.ok ? state.cycleEndsAtMs : null,
      synced: true,
    };
  }

  return {
    ok: state.ok,
    media,
    nextMedia,
    offsetMs: state.ok ? state.offsetMs : 0,
    durationMs: state.ok ? state.durationMs : 0,
    itemEndsAtMs: state.ok ? state.itemEndsAtMs : null,
    cycleEndsAtMs: state.ok ? state.cycleEndsAtMs : null,
    synced: false,
  };
}

export default function App() {
  const clockRef = useRef(new ClockSync(BASE_URL));
  const etagRef = useRef<string | null>(null);

  const [, setTick] = useState(0);
  const [apiState, setApiState] = useState<StateResponse | null>(null);
  const [media, setMedia] = useState<Media[]>([]);
  const [stateError, setStateError] = useState<string | null>(null);

  const refreshMedia = useCallback(async () => {
    const list = await listMedia(BASE_URL);
    setMedia(list);
  }, []);

  // Clock sync: once on load, then every 60 seconds.
  useEffect(() => {
    let cancelled = false;
    const sync = async () => {
      await clockRef.current.refresh();
      if (!cancelled) setTick((t) => t + 1);
    };
    sync();
    const id = setInterval(sync, 60_000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  // Poll /api/state every 2 seconds, using the ETag from the last response
  // so an unchanged state is a cheap 304 instead of a full payload.
  useEffect(() => {
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await getState(BASE_URL, etagRef.current ?? undefined);
        if (cancelled) return;
        if (result) {
          setApiState(result.state);
          etagRef.current = result.etag;
        }
        setStateError(null);
      } catch (err) {
        if (!cancelled) setStateError(err instanceof Error ? err.message : "Failed to load state");
      }
    };
    poll();
    const id = setInterval(poll, 2_000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  useEffect(() => {
    refreshMedia().catch(() => {
      // A failed media-list refresh just leaves the "add to window" picker
      // showing whatever it had before; the next successful poll retries.
    });
  }, [refreshMedia]);

  // The fast local tick: forces a re-render every 250ms so the scheduler
  // recomputes what's on screen continuously, not just when new data
  // arrives from the server.
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 250);
    return () => clearInterval(id);
  }, []);

  const handleAddMedia = useCallback(
    async (input: { name: string; type: MediaType; url?: string; duration_seconds: number }) => {
      await createMedia(BASE_URL, input);
      await refreshMedia();
    },
    [refreshMedia]
  );

  const handleAddToWindow = useCallback(async (windowId: string, mediaId: string) => {
    const res = await addPlaylistItem(BASE_URL, windowId, mediaId);
    const appliesAt = new Date(res.effective_at).toLocaleTimeString();
    return `Applies at ${appliesAt}.`;
  }, []);

  const handleTriggerSync = useCallback(async (mediaId: string, durationSeconds: number) => {
    await createSync(BASE_URL, mediaId, durationSeconds);
  }, []);

  const handleCancelSync = useCallback(async () => {
    await cancelSync(BASE_URL);
  }, []);

  if (!apiState) {
    return (
      <div className="app-status">
        {stateError ? `Could not reach the backend: ${stateError}` : "Loading…"}
      </div>
    );
  }

  const serverNowMs = clockRef.current.serverNow();

  return (
    <div className="app">
      <h1 className="app__title">Multi-Window Media Sequencer</h1>
      <SyncBanner sync={apiState.sync} serverNowMs={serverNowMs} />

      <div className="app__grid">
        {apiState.windows.map((w) => {
          const display = computeWindowDisplay(w, apiState.sync, apiState.cycle_seconds, serverNowMs);
          return (
            <MediaWindow
              key={w.id}
              windowName={w.name}
              ok={display.ok}
              media={display.media}
              nextMedia={display.nextMedia}
              offsetMs={display.offsetMs}
              durationMs={display.durationMs}
              itemEndsAtMs={display.itemEndsAtMs}
              cycleEndsAtMs={display.cycleEndsAtMs}
              serverNowMs={serverNowMs}
              synced={display.synced}
            />
          );
        })}
      </div>

      <ControlPanel
        media={media}
        windows={apiState.windows}
        syncActive={apiState.sync !== null}
        onAddMedia={handleAddMedia}
        onAddToWindow={handleAddToWindow}
        onTriggerSync={handleTriggerSync}
        onCancelSync={handleCancelSync}
      />
    </div>
  );
}
