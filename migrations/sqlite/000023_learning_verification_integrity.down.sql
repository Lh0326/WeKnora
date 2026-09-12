-- Dropping columns does not reinstate automation-made human approvals. Re-review is required.
-- Verification provenance; legacy rows remain unverified.
ALTER TABLE learning_task_attempts DROP COLUMN scorer_version;
ALTER TABLE learning_task_attempts DROP COLUMN rubric_version;
ALTER TABLE learning_task_attempts DROP COLUMN contract_version;
ALTER TABLE learning_task_attempts DROP COLUMN objective_version;
ALTER TABLE learning_quiz_attempts DROP COLUMN item_content_version;
ALTER TABLE learning_quiz_attempts DROP COLUMN scorer_version;
ALTER TABLE learning_quiz_attempts DROP COLUMN rubric_version;
ALTER TABLE learning_quiz_attempts DROP COLUMN contract_version;
ALTER TABLE learning_tasks DROP COLUMN evidence_hash;
ALTER TABLE learning_tasks DROP COLUMN objective_version;
ALTER TABLE learning_quiz_items DROP COLUMN objective_version;
ALTER TABLE learning_objectives DROP COLUMN evidence_hash;
