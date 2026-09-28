-- Each word names its speaker, so the editor never shows a host word as the guest.
ALTER TABLE words ADD COLUMN speaker TEXT NOT NULL DEFAULT '';
