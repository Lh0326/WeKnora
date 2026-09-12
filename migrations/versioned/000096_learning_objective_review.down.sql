ALTER TABLE learning_objectives
    DROP COLUMN IF EXISTS change_kind;
ALTER TABLE learning_objectives
    DROP COLUMN IF EXISTS review_note;
ALTER TABLE learning_objectives
    DROP COLUMN IF EXISTS published_at;
ALTER TABLE learning_objectives
    DROP COLUMN IF EXISTS reviewer;
