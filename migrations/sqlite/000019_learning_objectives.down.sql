DROP INDEX IF EXISTS idx_learning_quiz_attempts_objective;

ALTER TABLE learning_quiz_attempts DROP COLUMN content_version;
ALTER TABLE learning_quiz_attempts DROP COLUMN family_id;
ALTER TABLE learning_quiz_attempts DROP COLUMN objective_id;

ALTER TABLE learning_quiz_items DROP COLUMN family_id;
ALTER TABLE learning_quiz_items DROP COLUMN objective_id;

DROP TABLE IF EXISTS learning_objectives;
