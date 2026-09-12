-- Backfill idempotency moved out of learning_events (Lite). Mirrors versioned/000089.
--
-- Same rationale: the backfill_cite rows lived in a table profile deletion
-- empties, so a delete + restart resurrected the profile. This per-scope
-- tombstone survives subject-level deletion; the seed keeps scopes already
-- backfilled under the old event-based idempotency from re-appending.

CREATE TABLE IF NOT EXISTS learning_backfill_marks (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    completed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_backfill_marks_scope
    ON learning_backfill_marks (tenant_id, subject_id, knowledge_base_id);

INSERT OR IGNORE INTO learning_backfill_marks (tenant_id, subject_id, knowledge_base_id, completed_at)
SELECT DISTINCT tenant_id, subject_id, knowledge_base_id, CURRENT_TIMESTAMP
FROM learning_events
WHERE event_type = 'backfill_cite';
