-- Deletion-epoch fence (Lite). Mirrors versioned/000091.
--
-- One row per subject, bumped by the atomic profile-delete transaction;
-- background write paths compare it inside their write transaction so an
-- in-flight adjudication cannot resurrect a deleted profile.

CREATE TABLE IF NOT EXISTS learning_subject_epochs (
    subject_id VARCHAR(512) PRIMARY KEY,
    epoch INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
