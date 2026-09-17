-- Creates every table this application needs. It only ever runs once per
-- database: internal/db/migrate.go records each migration file's name in
-- schema_migrations after it succeeds, and skips files already recorded.

CREATE TABLE IF NOT EXISTS media (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    type              TEXT NOT NULL CHECK (type IN ('image', 'video', 'blank')),
    url               TEXT,
    duration_seconds  INT NOT NULL CHECK (duration_seconds > 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Blank media has no file to show, so it must have no URL; every other
    -- type must point somewhere.
    CONSTRAINT media_blank_has_no_url CHECK ((type = 'blank') = (url IS NULL))
);

CREATE TABLE IF NOT EXISTS windows (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    cycle_anchor  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Each row is one saved copy of a window's playlist. A new row is added
-- whenever the playlist changes; old rows are never edited or deleted, so
-- we can always answer "what was/will be playing at time T".
CREATE TABLE IF NOT EXISTS playlist_versions (
    id            BIGSERIAL PRIMARY KEY,
    window_id     TEXT NOT NULL REFERENCES windows(id),
    effective_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Looking up "the latest version that has already taken effect" is the
-- single most common query in this system (every schedule computation
-- needs it), so it gets its own index.
CREATE INDEX IF NOT EXISTS idx_playlist_versions_window_effective
    ON playlist_versions (window_id, effective_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS playlist_items (
    version_id  BIGINT NOT NULL REFERENCES playlist_versions(id),
    position    INT NOT NULL,
    media_id    TEXT NOT NULL REFERENCES media(id),
    PRIMARY KEY (version_id, position)
);

-- Records "show this one media item on every window at once" actions. Only
-- one is ever active (cancelled_at IS NULL and not yet expired) at a time.
CREATE TABLE IF NOT EXISTS sync_events (
    id                BIGSERIAL PRIMARY KEY,
    media_id          TEXT NOT NULL REFERENCES media(id),
    starts_at         TIMESTAMPTZ NOT NULL,
    duration_seconds  INT NOT NULL CHECK (duration_seconds > 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancelled_at      TIMESTAMPTZ
);

-- Finding "is there a currently-active or about-to-start sync" only ever
-- cares about rows that haven't been cancelled, so the index only covers
-- those (a partial index), keeping it small.
CREATE INDEX IF NOT EXISTS idx_sync_events_active_starts_at
    ON sync_events (starts_at)
    WHERE cancelled_at IS NULL;
