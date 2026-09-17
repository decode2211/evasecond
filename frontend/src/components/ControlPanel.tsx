// ControlPanel is the "remote control" for the whole system: the forms an
// operator uses to register new media, add media to a window's playlist,
// and start or stop synced playback. It doesn't call the backend directly —
// it only collects what the user typed and hands it to callback props, so
// App.tsx (which owns the actual API calls and the app's data) stays the
// single source of truth for what's really happening.
import { useState } from "react";
import type { Media, MediaType, WindowState } from "../api/client";

export interface ControlPanelProps {
  media: Media[];
  windows: WindowState[];
  syncActive: boolean;
  onAddMedia: (input: { name: string; type: MediaType; url?: string; duration_seconds: number }) => Promise<void>;
  onAddToWindow: (windowId: string, mediaId: string) => Promise<string>; // resolves with a human-readable "applies at" message
  onTriggerSync: (mediaId: string, durationSeconds: number) => Promise<void>;
  onCancelSync: () => Promise<void>;
}

// A small helper so every form in this panel reports errors and
// in-progress state the same way, instead of each one reinventing it.
function useAsyncAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  async function run(action: () => Promise<string | void>) {
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const result = await action();
      if (result) setMessage(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return { busy, error, message, run };
}

export function ControlPanel(props: ControlPanelProps) {
  const { media, windows, syncActive, onAddMedia, onAddToWindow, onTriggerSync, onCancelSync } = props;

  // "Add new media" form state.
  const [newName, setNewName] = useState("");
  const [newType, setNewType] = useState<MediaType>("image");
  const [newUrl, setNewUrl] = useState("");
  const [newDuration, setNewDuration] = useState(10);
  const addMediaAction = useAsyncAction();

  // "Add media to a window" form state.
  const [targetWindowId, setTargetWindowId] = useState(windows[0]?.id ?? "");
  const [targetMediaId, setTargetMediaId] = useState(media[0]?.id ?? "");
  const addToWindowAction = useAsyncAction();

  // "Sync playback" form state.
  const [syncMediaId, setSyncMediaId] = useState(media[0]?.id ?? "");
  const [syncDuration, setSyncDuration] = useState(30);
  const syncAction = useAsyncAction();
  const cancelSyncAction = useAsyncAction();

  function handleAddMedia(e: React.FormEvent) {
    e.preventDefault();
    addMediaAction.run(async () => {
      await onAddMedia({
        name: newName,
        type: newType,
        url: newType === "blank" ? undefined : newUrl,
        duration_seconds: newDuration,
      });
      setNewName("");
      setNewUrl("");
      return "Media created.";
    });
  }

  function handleAddToWindow(e: React.FormEvent) {
    e.preventDefault();
    if (!targetWindowId || !targetMediaId) return;
    addToWindowAction.run(() => onAddToWindow(targetWindowId, targetMediaId));
  }

  function handleTriggerSync(e: React.FormEvent) {
    e.preventDefault();
    if (!syncMediaId) return;
    syncAction.run(async () => {
      await onTriggerSync(syncMediaId, syncDuration);
      return "Sync started.";
    });
  }

  function handleCancelSync() {
    cancelSyncAction.run(async () => {
      await onCancelSync();
      return "Sync cancelled.";
    });
  }

  return (
    <div className="control-panel">
      <h2 className="control-panel__title">Controls</h2>

      <form className="control-panel__section" onSubmit={handleAddMedia}>
        <h3>Add new media</h3>
        <label>
          Name
          <input value={newName} onChange={(e) => setNewName(e.target.value)} required />
        </label>
        <label>
          Type
          <select value={newType} onChange={(e) => setNewType(e.target.value as MediaType)}>
            <option value="image">image</option>
            <option value="video">video</option>
            <option value="blank">blank</option>
          </select>
        </label>
        {newType !== "blank" && (
          <label>
            URL
            <input value={newUrl} onChange={(e) => setNewUrl(e.target.value)} required />
          </label>
        )}
        <label>
          Duration (seconds)
          <input
            type="number"
            min={1}
            value={newDuration}
            onChange={(e) => setNewDuration(Number(e.target.value))}
            required
          />
        </label>
        <button type="submit" disabled={addMediaAction.busy}>
          {addMediaAction.busy ? "Creating…" : "Create media"}
        </button>
        {addMediaAction.error && <p className="control-panel__error">{addMediaAction.error}</p>}
        {addMediaAction.message && <p className="control-panel__success">{addMediaAction.message}</p>}
      </form>

      <form className="control-panel__section" onSubmit={handleAddToWindow}>
        <h3>Add media to a window</h3>
        <label>
          Window
          <select value={targetWindowId} onChange={(e) => setTargetWindowId(e.target.value)}>
            {windows.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Media
          <select value={targetMediaId} onChange={(e) => setTargetMediaId(e.target.value)}>
            {media.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name} ({m.type})
              </option>
            ))}
          </select>
        </label>
        <button type="submit" disabled={addToWindowAction.busy || !targetWindowId || !targetMediaId}>
          {addToWindowAction.busy ? "Adding…" : "Add to playlist"}
        </button>
        {addToWindowAction.error && <p className="control-panel__error">{addToWindowAction.error}</p>}
        {addToWindowAction.message && <p className="control-panel__success">{addToWindowAction.message}</p>}
      </form>

      <form className="control-panel__section" onSubmit={handleTriggerSync}>
        <h3>Sync playback</h3>
        <label>
          Media
          <select value={syncMediaId} onChange={(e) => setSyncMediaId(e.target.value)}>
            {media.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name} ({m.type})
              </option>
            ))}
          </select>
        </label>
        <label>
          Duration (seconds)
          <input
            type="number"
            min={1}
            value={syncDuration}
            onChange={(e) => setSyncDuration(Number(e.target.value))}
            required
          />
        </label>
        <div className="control-panel__button-row">
          <button type="submit" disabled={syncAction.busy || !syncMediaId}>
            {syncAction.busy ? "Starting…" : "Start sync"}
          </button>
          <button type="button" onClick={handleCancelSync} disabled={cancelSyncAction.busy || !syncActive}>
            {cancelSyncAction.busy ? "Cancelling…" : "Cancel sync"}
          </button>
        </div>
        {(syncAction.error || cancelSyncAction.error) && (
          <p className="control-panel__error">{syncAction.error ?? cancelSyncAction.error}</p>
        )}
        {(syncAction.message || cancelSyncAction.message) && (
          <p className="control-panel__success">{syncAction.message ?? cancelSyncAction.message}</p>
        )}
      </form>
    </div>
  );
}
