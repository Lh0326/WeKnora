-- User-declared skip list (Lite). Mirrors versioned/000090.
--
-- Same rationale: a skip is a standing per-subject preference ("已掌握，
-- 不再推荐"), not foldable evidence — its own reversible table rather than
-- a learning_events row that view windows would age out.

CREATE TABLE IF NOT EXISTS learning_skips (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_skips_scope
    ON learning_skips (tenant_id, subject_id, knowledge_base_id, slug);
CREATE INDEX IF NOT EXISTS idx_learning_skips_scope_subject
    ON learning_skips (subject_id);
