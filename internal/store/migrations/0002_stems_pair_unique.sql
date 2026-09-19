-- One stem pair per episode. The upload completion races with itself on
-- retry, and the pair index keeps the first row per role. The delete keeps
-- the oldest row per pair, because running jobs already read the first one.
DELETE FROM stems WHERE rowid NOT IN (
    SELECT MIN(rowid) FROM stems GROUP BY episode_id, role
);
CREATE UNIQUE INDEX IF NOT EXISTS stems_episode_role_unique ON stems (episode_id, role);
