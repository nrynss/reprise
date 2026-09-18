-- Guest browser sessions. Each row belongs to one user row, and a revoke
-- marks it dead on the next request. The users table lives under another
-- namespace, so open that store before this one.
CREATE TABLE guest_sessions (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    created_at INTEGER NOT NULL,
    revoked INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX guest_sessions_user_idx ON guest_sessions (user_id);
