-- A row the embedding provider rejects, while others in the same pass are
-- embedded, is set aside under that model so it no longer blocks the rows
-- behind it (design.md section 10). It stays searchable by text and is tried
-- again under another model, after loci embed --backfill, or, for a message,
-- when its content changes.

ALTER TABLE memories ADD COLUMN embedding_failed TEXT;

ALTER TABLE messages ADD COLUMN embedding_failed TEXT;
