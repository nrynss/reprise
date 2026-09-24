-- The render the stored rendered words came from. Analysis replaces the
-- rendered words whole on each run, so one row per episode names the
-- render behind them. A reader compares it with the newest render, so a
-- new render never plays under old words. Both ends cascade, so deleting
-- the episode or the render removes the claim. The row names no owner.
-- The episode already carries it, so moving an episode to a new owner
-- leaves nothing here pointing at the old one.

CREATE TABLE rendered_sources (
    episode_id TEXT NOT NULL PRIMARY KEY REFERENCES episodes (id) ON DELETE CASCADE,
    render_id TEXT NOT NULL REFERENCES renders (id) ON DELETE CASCADE
);
CREATE INDEX rendered_sources_render_idx ON rendered_sources (render_id);
