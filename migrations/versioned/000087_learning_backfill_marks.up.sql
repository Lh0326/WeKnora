-- Migration 000087: backfill idempotency moved out of learning_events.
--
-- The startup backfill used the backfill_cite event rows themselves as its
-- idempotency set. DeleteLearningDataBySubject removes those rows, so a
-- profile delete followed by a restart re-derived the same history from the
-- (untouched) memory doc-affinity table and re-folded it: the deleted
-- learning profile silently resurrected. learning_backfill_marks is the
-- independent per-scope tombstone: one row means "this person's history in
-- this KB was already replayed", it survives profile deletion like the
-- opt-out prefs do, and it is removed only by the orphan-KB sweep.
--
-- The seed preserves the old idempotency for scopes already backfilled:
-- without it, every existing deployment would re-append its full backfill
-- history once after upgrading, doubling the folded weights.

CREATE TABLE IF NOT EXISTS learning_backfill_marks (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    completed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_backfill_marks_scope
    ON learning_backfill_marks (tenant_id, subject_id, knowledge_base_id);

INSERT INTO learning_backfill_marks (tenant_id, subject_id, knowledge_base_id, completed_at)
SELECT DISTINCT tenant_id, subject_id, knowledge_base_id, CURRENT_TIMESTAMP
FROM learning_events
WHERE event_type = 'backfill_cite'
ON CONFLICT DO NOTHING;
