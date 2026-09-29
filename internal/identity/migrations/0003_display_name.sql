-- Display name for shared episodes. Empty means the owner never set one.
-- The share payload reads it as the author, and account deletion removes
-- the user row, so the name goes with the account.
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
