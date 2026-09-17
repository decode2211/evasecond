# Multi-Window Media Sequencer with Sync Playback

## 1. What this is

A full-stack app where several independent "display windows" each loop
through their own list of images and videos forever, on a fixed 5-hour
cycle. An operator can add media to any window's list at any time, and can
trigger a "sync" that makes every window show the same media item at the
same instant, then returns each window to its own normal schedule. The
backend is Go + PostgreSQL; the frontend is React + TypeScript; both are
built to be deployed for free (Render + Neon + Vercel).

**Live URLs:**
- Frontend: `<TODO: fill in after deploying to Vercel>`
- Backend API: `<TODO: fill in after deploying to Render>`

## 2. How it works, in plain English

Think of each window as its own small TV channel with a fixed program
schedule that repeats every 5 hours, like a looping in-store advertising
reel. Nobody has to "press play" — anyone who looks at the screen at any
moment can work out exactly what should be showing, purely from the
schedule and the current time, the same way you can glance at a printed TV
guide and know what's on right now without watching a live feed.

That's also how this app is built: there is no "give me the next clip"
button on the server. Every window's playlist, plus the current time, is
enough information for anyone — the server or any browser — to compute
independently what should be on screen right this second, and how far into
it we are.

```
   Window's playlist (a list of clips + how long each one plays)
              │
              ▼
   ┌─────────────────────────────────────────────┐
   │   scheduler:  given "now", work out          │
   │   which clip is playing and the offset       │
   │   into it — pure math, no state, no          │
   │   "current position" stored anywhere         │
   └─────────────────────────────────────────────┘
              │
      ┌───────┴────────┐
      ▼                 ▼
  Go backend        Browser (TypeScript)
  (same algorithm,  (same algorithm, ported
   used for /now     line-for-line, used to
   and validation)   drive what's on screen)
```

Both the backend and the frontend run the *exact same scheduling
algorithm* — one written in Go, one ported to TypeScript — so they always
agree on what should be playing, and the browser never needs to poll the
server every second just to know "what's next".

## 3. The 5-hour cycle, with a worked example

Every window has a `cycle_anchor` (the moment its very first cycle began)
and cycles are a fixed length, `CYCLE_SECONDS` (18000 seconds = 5 hours by
default, configurable via an env var). Within one cycle, the window's
playlist plays start-to-finish and then loops back to item 1 — again, and
again — until the 5-hour mark is reached, at which point playback
**restarts from item 1 no matter where it was**, even if that cuts an item
short mid-way through.

**Worked example:** a window's playlist is 10s + 20s + 30s long (60
seconds, one full "loop"). 75 seconds have passed since the loop started.
`75 mod 60 = 15`, so we're 15 seconds into the *current pass* of the loop.
Walking through the items: the first item covers 0–10s, the second covers
10–30s. 15 seconds falls inside the second item, 5 seconds into it
(`15 − 10 = 5`). So right now, this window is showing item 2, five seconds
in.

**Worked example, cycle boundary:** if that same window's playlist were
10,000,000ms + 9,000,000ms long (loopLen 19s short of the 18,000,000ms
cycle) and we're 1 second away from the 5-hour mark, the currently-playing
item gets cut off exactly at the 5-hour mark and item 1 starts again from
zero — the schedule doesn't wait for the current item to finish first.

This math lives in one place on each side — `backend/internal/schedule`
(Go) and `frontend/src/schedule` (TypeScript) — and both are proven to
agree via a shared set of test cases in `testdata/schedule_vectors.json`
that both test suites load and run.

## 4. Sync behavior

**Triggering it:** `POST /api/sync` with `{"media_id": "...",
"duration_seconds": N}`. Only one sync can be active at a time — starting
a new one immediately cancels whatever was running.

**Why there's a lead time:** the sync doesn't start the instant the
request is made. Instead, `starts_at` is set to `now + SYNC_LEAD_MS`
(1500ms by default). Every browser is polling `/api/state` every couple of
seconds, so without a lead time, different browsers would learn about the
sync at slightly different real-world moments (network latency, poll
timing) and would start playing the synced item at different times relative
to each other — defeating the whole point of "sync". A short lead time
means every browser has a chance to receive the instruction *before* the
moment it's supposed to happen, so they all switch in lockstep.

**How windows resume — the "wall-clock" model:** while a sync is active, a
window's own playlist timeline is **not paused** — it keeps running
underneath, exactly as if the sync weren't happening. When the sync ends,
each window simply goes back to showing whatever its own schedule says is
"current" *right now*. It does not rewind to resume from the exact point it
was interrupted.

- **Why this way:** it keeps the "what's on screen is a pure function of
  time" property intact for everything, including sync. No window ever
  needs a special "I was paused here" state saved anywhere, which means a
  server restart, a new browser tab, or a totally different device all
  compute the identical answer with zero extra bookkeeping.
- **The tradeoff:** a window that gets synced effectively "loses" however
  many seconds the sync lasted from its own content — it skips ahead rather
  than catching up. The alternative, **pause-and-resume** (freeze each
  window's position when sync starts, continue from exactly there when it
  ends), would show every configured item eventually, but at the cost of
  each window's cycle timing drifting out of sync with its own
  `cycle_anchor` math, and would require persisting "how much time was
  paused" as real state instead of pure computation. For a display-wall use
  case where perfect long-run schedule accuracy matters more than never
  skipping a few seconds of one item, wall-clock resume was the simpler and
  more robust choice.

## 5. Dynamic playlist changes

`POST /api/windows/{id}/items` adds one media item to a window's playlist.
Playlists are never edited in place — adding an item creates a brand new
**playlist version** (a full copy of the old item list plus the new item),
tagged with an `effective_at` timestamp. Until that moment arrives, the
window keeps playing its old version exactly as before; the new version
takes over automatically the instant `effective_at` passes.

**Why not apply it immediately?** Because doing so would yank whatever is
currently on screen and restart the loop from item 1 mid-item, which is
exactly the jarring behavior the assignment says to avoid. So
`effective_at` is always set to the *next natural boundary*: either the
next time the current loop would restart from item 1 anyway, or the next
5-hour cycle boundary — whichever comes first.

**Stacking changes:** if you add a second item before the first change has
even taken effect, the new version is built on top of the *latest known*
version (including that still-pending one) — not the one currently
playing — so both additions end up together in one final version, and the
second addition's `effective_at` is never scheduled earlier than the first
one already promised.

## 6. Architecture & folder structure

```
backend/
  cmd/server/main.go        entry point: config, DB, migrations, seed, HTTP server, graceful shutdown
  internal/config/          env var loading + validation
  internal/db/              connection pool, embedded SQL migrations, seed data
  internal/models/          Media, Window, PlaylistVersion, SyncEvent structs
  internal/schedule/        PURE scheduling functions — no DB, no HTTP (schedule.go + schedule_test.go)
  internal/store/           all SQL lives here
  internal/httpapi/         HTTP handlers, routing, JSON helpers, CORS, errors
  Dockerfile                multi-stage, non-root, ~19MB final image
frontend/
  src/api/client.ts         typed API client — the only file that calls fetch()
  src/clock/offset.ts       estimates the offset between the browser's clock and the server's
  src/schedule/schedule.ts  TS port of the Go scheduler — must always agree with it
  src/components/           MediaWindow, ControlPanel, SyncBanner
  src/App.tsx, main.tsx     ties everything together: polling, ticking, rendering
testdata/schedule_vectors.json   shared test cases run by BOTH the Go and TS test suites
docker-compose.yml               local Postgres for development
scripts/smoke_test.sh            curl-based end-to-end check against a running instance
render.yaml                      Render Blueprint for the backend
```

Backend stack: Go 1.23, stdlib `net/http` with Go 1.22+ method-pattern
routing (no web framework), PostgreSQL via `pgx/v5`, SQL migrations
embedded with `embed` and applied idempotently on startup, `log/slog` for
structured logs.

Frontend stack: React 18 + Vite + TypeScript, no UI framework — one plain
CSS file.

## 7. Local setup (Git Bash)

Three terminal windows, each running one process in the foreground.

**Window 1 — Postgres:**
```bash
cd /e/eva2
docker compose up
```
Wait for `database system is ready to accept connections`. Stop with
`Ctrl+C` (data persists in the `eva2_postgres_data` volume between runs).

**Window 2 — backend:**
```bash
cd /e/eva2/backend
DATABASE_URL="postgres://postgres:postgres@localhost:5433/eva2?sslmode=disable" PORT=8099 CORS_ORIGINS="http://localhost:5173" go run ./cmd/server
```
Wait for a JSON log line ending `"msg":"listening","port":"8099"`. The
first run after any code change takes ~15–20s to compile before anything
is printed — that's normal. Stop with `Ctrl+C`.

**Window 3 — frontend:**
```bash
cd /e/eva2/frontend
cp .env.example .env   # first time only — then edit VITE_API_BASE_URL if needed
npm install             # first time only
npm run dev
```
Wait for Vite to print `Local: http://localhost:5173/`. Stop with
`Ctrl+C`. Then open that URL in a browser.

## 8. Environment variables

**Backend** (`backend/.env.example`):

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | TCP port the HTTP server listens on |
| `DATABASE_URL` | *(required)* | Postgres connection string |
| `CORS_ORIGINS` | *(empty)* | Comma-separated list of frontend origins allowed to call the API from a browser |
| `CYCLE_SECONDS` | `18000` | Length of one playlist cycle, in seconds (5 hours) |
| `SYNC_LEAD_MS` | `1500` | How far in the future a new sync's `starts_at` is set |
| `SEED_ON_START` | `true` | Insert placeholder demo data the first time the DB is empty |

**Frontend** (`frontend/.env.example`):

| Variable | Meaning |
|---|---|
| `VITE_API_BASE_URL` | Base URL of the backend API, no trailing slash |

## 9. API documentation

All responses include `server_time` (except where noted) as an RFC3339
timestamp with millisecond precision. Errors are always
`{"error": {"code": "...", "message": "..."}}`.

**`GET /health`**
```bash
curl http://localhost:8099/health
# {"status":"ok","db":"ok"}
```

**`GET /api/time`** — used by clients to estimate their clock offset from the server.
```bash
curl http://localhost:8099/api/time
# {"server_time":"2026-09-17T20:49:04.123Z"}
```

**`GET /api/state`** — everything a client needs to render every window. Supports `ETag` / `If-None-Match` → `304 Not Modified` when nothing has changed.
```bash
curl http://localhost:8099/api/state
curl -H 'If-None-Match: "<etag from previous response>"' http://localhost:8099/api/state   # -> 304 if unchanged
```

**`GET /api/media`**
```bash
curl http://localhost:8099/api/media
```

**`POST /api/media`** — register a new media item. `url` is required for `image`/`video`, forbidden for `blank`.
```bash
curl -X POST http://localhost:8099/api/media \
  -H 'Content-Type: application/json' \
  -d '{"name":"My Clip","type":"video","url":"https://example.com/clip.mp4","duration_seconds":15}'
```

**`POST /api/windows/{id}/items`** — add one media item to a window's playlist, returning the new version and when it takes effect.
```bash
curl -X POST http://localhost:8099/api/windows/W1/items \
  -H 'Content-Type: application/json' \
  -d '{"media_id":"M1"}'
# {"version":{...},"effective_at":"2026-09-17T21:07:06.000Z"}
```

**`GET /api/windows/{id}/now`** — server-computed "what's playing right now", mainly for debugging/verification.
```bash
curl http://localhost:8099/api/windows/W1/now
```

**`POST /api/sync`** — show one media item on every window at once.
```bash
curl -X POST http://localhost:8099/api/sync \
  -H 'Content-Type: application/json' \
  -d '{"media_id":"M2","duration_seconds":30}'
```

**`DELETE /api/sync`** — cancel the active/upcoming sync (no-op if none).
```bash
curl -X DELETE http://localhost:8099/api/sync
# 204 No Content
```

## 10. Deployment

1. **Database (Neon):** create a free Neon Postgres project, copy its
   connection string (make sure it includes `sslmode=require`).
2. **Backend (Render):** create a new Web Service from this repo using the
   included `render.yaml` (Render will detect it as a Blueprint). Set
   `DATABASE_URL` to the Neon connection string and leave `CORS_ORIGINS`
   blank for now — you'll fill it in after step 3. Render will build
   `backend/Dockerfile` and health-check `/health`.
   - **Cold starts:** Render's free tier spins down an idle service. The
     first request after a period of inactivity can take **30–50 seconds**
     to respond while it wakes back up — this is expected, not a bug.
3. **Frontend (Vercel):** import this repo, set the project root to
   `frontend/`, and set the env var `VITE_API_BASE_URL` to your Render
   backend's URL. `frontend/vercel.json` handles SPA routing.
4. **Close the loop:** go back to the Render service and set
   `CORS_ORIGINS` to your Vercel frontend's URL (e.g.
   `https://your-app.vercel.app`), then redeploy the backend so the new
   value takes effect.
5. Verify everything with the smoke test:
   ```bash
   scripts/smoke_test.sh https://your-backend.onrender.com
   ```

## 11. Testing

- **Go — pure scheduler:** `cd backend && go test ./internal/schedule/...`
  — table-driven tests covering cycle start, mid-item, exact boundaries,
  loop wrap, the 5-hour cut, version switches, empty playlists, and every
  sync phase (upcoming/active/just-ended). Also loads
  `testdata/schedule_vectors.json`.
- **Go — HTTP integration:** `cd backend && TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5433/eva2?sslmode=disable go test ./internal/httpapi/...`
  — real HTTP requests through the real router against a real Postgres
  database (add item, sync create/cancel, validation errors). Skips itself
  with a clear message if `TEST_DATABASE_URL` isn't set.
- **TypeScript — scheduler:** `cd frontend && npm test` — runs the exact
  same `testdata/schedule_vectors.json` cases through the TS port, proving
  the two implementations agree.
- **Smoke test:** `scripts/smoke_test.sh <BASE_URL>` — an end-to-end curl
  script: health, state, create media, add to playlist, validation
  rejection, start sync, confirm every window shows the synced media,
  cancel sync, confirm resumption.

Run `go vet ./...` and `npm run build` before considering any change done.

## 12. Assumptions and tradeoffs

- **Placeholder seed data:** the assignment PDF describes the scenario in
  prose and asks for "seed data matching the example windows and media
  lists," but doesn't actually contain a concrete example table — only a
  generic mention of "M2" in the sync explanation. Seed data (3 windows,
  4 media items + a blank slot) was designed from scratch to exercise
  every feature (repeated media across windows, a blank slot, differing
  playlist lengths).
- **Seed media hosting:** seed images use `placehold.co` and seed videos
  use MDN's CC0 sample-video host. Both were chosen and verified
  (`curl -I`, including with an `Origin` header) specifically for
  returning `Access-Control-Allow-Origin`, correct `Content-Type`, and
  (for video) `Accept-Ranges` + a working `206 Partial Content` response —
  after an earlier choice (Wikimedia Commons thumbnails, an old Google
  Cloud Storage sample bucket) turned out to be unreliable: one thumbnail
  URL 404'd and the Google bucket now returns 403 Forbidden.
- **Sync resume model:** wall-clock (see §4) rather than pause-and-resume,
  favoring a stateless, always-recomputable schedule over never skipping
  content during a sync.
- **Polling + ETag instead of SSE/WebSockets:** the frontend polls
  `/api/state` every 2 seconds with conditional GET rather than holding a
  persistent connection open. This is simpler, needs no special
  infrastructure on Render's free tier, and degrades gracefully — the
  worst case is state being up to ~2 seconds stale, which is invisible for
  a display-wall use case where the local scheduler is already filling in
  smooth playback between polls.
- **Clock offset accuracy:** the browser estimates the server's clock via
  a handful of `/api/time` round trips, keeping the lowest-latency sample
  (see `frontend/src/clock/offset.ts`). This is accurate to roughly the
  network's round-trip time, refreshed every 60 seconds — good enough for
  smooth on-screen timelines, not frame-perfect synchronization.
- **Muted autoplay:** all `<video>` elements are muted, which is required
  for browsers to autoplay them at all without a user gesture. This means
  synced or looping video content is silent by design.
- **Playlist version `effective_at` rule:** always the sooner of the next
  loop restart or the next cycle boundary, layered onto the latest known
  (possibly still-pending) version — see §5.

## 13. What I would improve with more time

- Push-based updates (SSE or WebSockets) instead of 2-second polling, for
  lower-latency playlist/sync propagation.
- Component and end-to-end UI tests (e.g. Playwright) — currently only the
  scheduler has automated tests on the frontend.
- A configurable pause-and-resume sync mode, as an alternative to the
  wall-clock model, for use cases where never skipping content matters
  more than perfect schedule determinism.
- Basic auth (or any auth) on the control endpoints — right now, anyone
  with the deployed backend URL can add media or trigger sync.
- Direct media upload instead of URL-only, with server-side validation
  that a URL actually serves a loadable image/video before accepting it.
- Per-window configurable cycle lengths instead of one global
  `CYCLE_SECONDS` for every window.
