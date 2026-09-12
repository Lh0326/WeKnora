DROP INDEX IF EXISTS idx_learning_task_attempts_task;
DROP INDEX IF EXISTS idx_learning_task_attempts_scope;
DROP TABLE IF EXISTS learning_task_attempts;
DROP TABLE IF EXISTS learning_tasks;

ALTER TABLE learning_quiz_attempts DROP COLUMN grade_reason;
ALTER TABLE learning_quiz_attempts DROP COLUMN eligible;
ALTER TABLE learning_quiz_attempts DROP COLUMN item_status;
ALTER TABLE learning_quiz_attempts DROP COLUMN assistance_mode;

ALTER TABLE learning_quiz_items DROP COLUMN change_kind;
ALTER TABLE learning_quiz_items DROP COLUMN review_note;
ALTER TABLE learning_quiz_items DROP COLUMN published_at;
ALTER TABLE learning_quiz_items DROP COLUMN reviewer;
ALTER TABLE learning_quiz_items DROP COLUMN family_fingerprint;
ALTER TABLE learning_quiz_items DROP COLUMN assistance_mode;
ALTER TABLE learning_quiz_items DROP COLUMN scorer_version;
ALTER TABLE learning_quiz_items DROP COLUMN rubric_version;
ALTER TABLE learning_quiz_items DROP COLUMN content_version;
