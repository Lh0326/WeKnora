-- Observable objectives and evidence separation (Lite). Mirrors versioned/000094.

CREATE TABLE IF NOT EXISTS learning_objectives (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    title VARCHAR(255) NOT NULL,
    behavior TEXT NOT NULL,
    capability_type VARCHAR(32) NOT NULL DEFAULT 'concept',
    contract_type VARCHAR(32) NOT NULL DEFAULT 'concept_two_family',
    contract_params TEXT,
    contract_version VARCHAR(64) NOT NULL DEFAULT '',
    source_refs TEXT,
    content_version VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    prereq_objective_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_objectives_scope
    ON learning_objectives (tenant_id, knowledge_base_id, slug, status);

ALTER TABLE learning_quiz_items ADD COLUMN objective_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN family_id VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE learning_quiz_attempts ADD COLUMN objective_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN family_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN content_version VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_learning_quiz_attempts_objective
    ON learning_quiz_attempts (tenant_id, subject_id, knowledge_base_id, objective_id);
