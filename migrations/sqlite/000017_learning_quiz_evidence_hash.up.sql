-- Quiz evidence versioning (Lite). Mirrors versioned/000090.

ALTER TABLE learning_quiz_items ADD COLUMN evidence_hash VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_learning_quiz_items_evidence
    ON learning_quiz_items (tenant_id, knowledge_base_id, slug, status, evidence_hash);
