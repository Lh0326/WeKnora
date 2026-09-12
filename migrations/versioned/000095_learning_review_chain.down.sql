DROP INDEX IF EXISTS idx_learning_task_attempts_task;
DROP INDEX IF EXISTS idx_learning_task_attempts_scope;
DROP TABLE IF EXISTS learning_task_attempts;
DROP TABLE IF EXISTS learning_tasks;

ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS grade_reason;
ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS eligible;
ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS item_status;
ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS assistance_mode;

ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS change_kind;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS review_note;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS published_at;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS reviewer;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS family_fingerprint;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS assistance_mode;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS scorer_version;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS rubric_version;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS content_version;
