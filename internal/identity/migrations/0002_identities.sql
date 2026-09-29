-- Login identities and one time sign in codes. An identity attaches a
-- login to the existing local user, so the user id never changes and the
-- diary stays reachable across devices. One user may hold several
-- identities, and one identity maps to one user.
CREATE TABLE identities (
    id TEXT NOT NULL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    provider TEXT NOT NULL CHECK (provider IN ('email', 'google')),
    subject TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (provider, subject)
);
CREATE INDEX identities_user_idx ON identities (user_id);

-- One time sign in codes. Both hashes use the login code key, so a stolen
-- database copy verifies nothing and reveals no address and no code. The
-- plain code never lands here. The plain address stays only while the send
-- still needs it, and later code clears it once the code is used or past
-- its expiry.
CREATE TABLE login_codes (
    id TEXT NOT NULL PRIMARY KEY,
    address_hash TEXT NOT NULL,
    address TEXT NOT NULL DEFAULT '',
    code_hash TEXT NOT NULL,
    requesting_session TEXT NOT NULL REFERENCES guest_sessions (id),
    expires_at INTEGER NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    used_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);
CREATE INDEX login_codes_address_created_idx ON login_codes (address_hash, created_at);
