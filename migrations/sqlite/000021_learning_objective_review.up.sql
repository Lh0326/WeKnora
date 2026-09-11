-- Objective review audit columns (Lite). Mirrors versioned/000094.

ALTER TABLE learning_objectives ADD COLUMN reviewer VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE learning_objectives ADD COLUMN published_at DATETIME;
ALTER TABLE learning_objectives ADD COLUMN review_note TEXT;
ALTER TABLE learning_objectives ADD COLUMN change_kind VARCHAR(16) NOT NULL DEFAULT '';
