-- The memory index. Resolution rows link a commitment mention to the
-- later doing mention that closes it. Both ends cascade, so deleting an
-- episode or a mention removes its resolutions. The diary tables stay in
-- the store namespace. This file owns only what the index adds.

CREATE TABLE resolutions (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    commitment_mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE,
    evidence_mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE
);
CREATE INDEX resolutions_commitment_idx ON resolutions (commitment_mention_id);
CREATE INDEX resolutions_evidence_idx ON resolutions (evidence_mention_id);
