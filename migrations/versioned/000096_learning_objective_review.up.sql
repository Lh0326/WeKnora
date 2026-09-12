-- Migration 000096: objective review audit columns (stage-2 completion).
--
-- ReviewObjective previously recorded only the status transition; the
-- audit trail (reviewer, published_at, note, change kind) now lands on
-- the definition row exactly like quiz items and tasks, so the whole
-- demo-path content package carries review records.

ALTER TABLE learning_objectives
    ADD COLUMN IF NOT EXISTS reviewer VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE learning_objectives
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE learning_objectives
    ADD COLUMN IF NOT EXISTS review_note TEXT;
ALTER TABLE learning_objectives
    ADD COLUMN IF NOT EXISTS change_kind VARCHAR(16) NOT NULL DEFAULT '';
