-- Display name for shared episodes. Empty means the owner never set one.
-- The share payload reads it as the author, and account deletion removes
-- the user row, so the name goes with the account.
-- The login set also runs where the diary tables are absent, so it stages
-- the parent users table before altering it. The normal boot opens the
-- diary store first, which leaves the staging as a no-op. The alter
-- backfills existing rows with empty, and copies that already applied
-- this file skip it through the ledger.
CREATE TABLE IF NOT EXISTS users (
    id TEXT NOT NULL PRIMARY KEY,
    kind TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
