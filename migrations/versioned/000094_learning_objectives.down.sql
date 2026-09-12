DROP INDEX IF EXISTS idx_learning_quiz_attempts_objective;

ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS content_version;
ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS family_id;
ALTER TABLE learning_quiz_attempts
    DROP COLUMN IF EXISTS objective_id;

ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS family_id;
ALTER TABLE learning_quiz_items
    DROP COLUMN IF EXISTS objective_id;

DROP TABLE IF EXISTS learning_objectives;
