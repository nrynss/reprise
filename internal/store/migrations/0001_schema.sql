-- The diary schema. Rows below an episode carry owner and episode, and
-- deleting an episode cascades to them. Spend and runtime switches stay in
-- the Keel stores that own them, so no table here repeats them.

CREATE TABLE users (
    id TEXT NOT NULL PRIMARY KEY,
    kind TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);

CREATE TABLE episodes (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    number INTEGER NOT NULL,
    title TEXT NOT NULL,
    state TEXT NOT NULL,
    visibility TEXT NOT NULL,
    share_token TEXT NOT NULL,
    seeded INTEGER NOT NULL DEFAULT 0,
    UNIQUE (owner_id, number)
);

CREATE TABLE sessions (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    provider_session_id TEXT NOT NULL,
    token_cap INTEGER NOT NULL,
    connected_seconds INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX sessions_episode_idx ON sessions (episode_id);

CREATE TABLE stems (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    media_id TEXT NOT NULL,
    role TEXT NOT NULL,
    sample_rate INTEGER NOT NULL,
    start_offset_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX stems_episode_idx ON stems (episode_id);

CREATE TABLE turns (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    text TEXT NOT NULL,
    started_ms INTEGER NOT NULL,
    ended_ms INTEGER NOT NULL,
    provider_item_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX turns_episode_idx ON turns (episode_id);

CREATE TABLE words (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    text TEXT NOT NULL,
    start_ms INTEGER NOT NULL,
    end_ms INTEGER NOT NULL,
    source TEXT NOT NULL
);
CREATE INDEX words_episode_idx ON words (episode_id);

CREATE TABLE proposals (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    start_word INTEGER NOT NULL,
    end_word INTEGER NOT NULL,
    reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX proposals_episode_idx ON proposals (episode_id);

CREATE TABLE decisions (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    proposal_id TEXT NOT NULL REFERENCES proposals (id) ON DELETE CASCADE,
    decision TEXT NOT NULL
);
CREATE INDEX decisions_episode_idx ON decisions (episode_id);
CREATE INDEX decisions_proposal_idx ON decisions (proposal_id);

CREATE TABLE renders (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    input_hash TEXT NOT NULL,
    opus_media_id TEXT NOT NULL,
    aac_media_id TEXT NOT NULL,
    loudness REAL NOT NULL
);
CREATE INDEX renders_episode_idx ON renders (episode_id);

CREATE TABLE analyses (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    transcript_id TEXT NOT NULL,
    chapters TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    entities TEXT NOT NULL DEFAULT '',
    key_phrases TEXT NOT NULL DEFAULT ''
);
CREATE INDEX analyses_episode_idx ON analyses (episode_id);

CREATE TABLE mentions (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    word_offset INTEGER NOT NULL,
    quote TEXT NOT NULL
);
CREATE INDEX mentions_episode_idx ON mentions (episode_id);

CREATE TABLE callbacks (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE,
    used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX callbacks_episode_idx ON callbacks (episode_id);
CREATE INDEX callbacks_mention_idx ON callbacks (mention_id);
