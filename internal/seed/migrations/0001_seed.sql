-- The shared catalog and its per-user receipts. Catalog rows belong to
-- the reserved seed user, and one row maps each stable key to its episode.
-- A receipt records that a visitor already received a copy, so the copy
-- never runs twice. Dropping copies never clears receipts. Both tables
-- cascade with the rows they point at, so the guest sweep never trips on
-- them: receipts leave with swept guests, and catalog rows leave with
-- retired catalog episodes.

CREATE TABLE seed_catalog (
    key TEXT NOT NULL PRIMARY KEY,
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    audio_blob_id TEXT NOT NULL DEFAULT ''
);

CREATE TABLE seed_receipts (
    user_id TEXT NOT NULL PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    copied_at INTEGER NOT NULL,
    episodes INTEGER NOT NULL
);
