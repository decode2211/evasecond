// This file is the ONLY place in the frontend that talks to the backend
// over HTTP. Every other part of the app (components, the clock, the
// scheduler) works with the plain TypeScript types defined here instead of
// raw fetch calls, so if the API ever changes shape, this is the one file
// that needs to change.

export type MediaType = "image" | "video" | "blank";

export interface Media {
  id: string;
  name: string;
  type: MediaType;
  url: string | null;
  duration_seconds: number;
  created_at: string;
}

export interface PlaylistItem {
  position: number;
  media_id: string;
  media?: Media;
}

export interface PlaylistVersion {
  id: number;
  effective_at: string;
  items: PlaylistItem[];
}

export interface WindowState {
  id: string;
  name: string;
  cycle_anchor: string;
  current_version: PlaylistVersion | null;
  upcoming_versions: PlaylistVersion[];
}

export interface SyncEvent {
  id: number;
  media_id: string;
  media?: Media;
  starts_at: string;
  duration_seconds: number;
}

export interface StateResponse {
  cycle_seconds: number;
  windows: WindowState[];
  sync: SyncEvent | null;
  server_time: string;
}

export interface TimeResponse {
  server_time: string;
}

export interface AddPlaylistItemResponse {
  version: PlaylistVersion;
  effective_at: string;
}

// ApiError is thrown for any non-2xx response. Its `code` matches the
// backend's stable error code (e.g. "validation_error"), so callers can
// branch on it instead of parsing the human-readable message.
export class ApiError extends Error {
  code: string;
  status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

async function request<T>(baseUrl: string, path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${baseUrl}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });

  if (!res.ok) {
    let code = "unknown_error";
    let message = `request to ${path} failed with status ${res.status}`;
    try {
      const body = await res.json();
      if (body?.error?.code) code = body.error.code;
      if (body?.error?.message) message = body.error.message;
    } catch {
      // Response body wasn't JSON (or was empty) — fall back to the
      // generic message above rather than failing to report an error.
    }
    throw new ApiError(res.status, code, message);
  }

  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export function getTime(baseUrl: string): Promise<TimeResponse> {
  return request<TimeResponse>(baseUrl, "/api/time");
}

// getState supports conditional GET: pass the ETag from a previous call and,
// if nothing has changed, the promise resolves to null instead of doing a
// full round trip's worth of JSON parsing on data the caller already has.
export async function getState(baseUrl: string, etag?: string): Promise<{ state: StateResponse; etag: string | null } | null> {
  const headers: Record<string, string> = {};
  if (etag) headers["If-None-Match"] = etag;

  const res = await fetch(`${baseUrl}/api/state`, { headers });

  if (res.status === 304) {
    return null;
  }
  if (!res.ok) {
    throw new ApiError(res.status, "unknown_error", `GET /api/state failed with status ${res.status}`);
  }
  const state = (await res.json()) as StateResponse;
  return { state, etag: res.headers.get("ETag") };
}

export function listMedia(baseUrl: string): Promise<Media[]> {
  return request<Media[]>(baseUrl, "/api/media");
}

export function createMedia(
  baseUrl: string,
  input: { name: string; type: MediaType; url?: string; duration_seconds: number }
): Promise<Media> {
  return request<Media>(baseUrl, "/api/media", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function addPlaylistItem(baseUrl: string, windowId: string, mediaId: string): Promise<AddPlaylistItemResponse> {
  return request<AddPlaylistItemResponse>(baseUrl, `/api/windows/${encodeURIComponent(windowId)}/items`, {
    method: "POST",
    body: JSON.stringify({ media_id: mediaId }),
  });
}

export function createSync(baseUrl: string, mediaId: string, durationSeconds: number): Promise<SyncEvent> {
  return request<SyncEvent>(baseUrl, "/api/sync", {
    method: "POST",
    body: JSON.stringify({ media_id: mediaId, duration_seconds: durationSeconds }),
  });
}

export function cancelSync(baseUrl: string): Promise<void> {
  return request<void>(baseUrl, "/api/sync", { method: "DELETE" });
}
