ALTER TABLE learning_quiz_attempts ADD COLUMN original_slug VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE learning_events ADD COLUMN original_slug VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE mastery_states ADD COLUMN projection_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE mastery_states ADD COLUMN replay_hash VARCHAR(64) NOT NULL DEFAULT '';
