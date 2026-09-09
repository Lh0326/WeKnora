DROP INDEX IF EXISTS idx_learning_quiz_items_evidence;

ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS evidence_hash;
