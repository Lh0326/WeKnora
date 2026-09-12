-- Migration 000094: observable objectives and evidence separation (stage 1).
--
-- Adds the objective definition layer (01 §4/§5/§11.3): wiki nodes keep
-- their page identity; learning_objectives adds the measurable "can do X"
-- layer with a frozen, versioned verification contract. Quiz items link to
-- the objective they verify and carry a FAMILY identity — evidence
-- independence is judged per family, not per item id. Quiz attempts freeze
-- objective/family/content-version at answer time (server-side), so later
-- item edits never rewrite what a historical attempt verified; rows
-- without the linkage are legacy and surface as legacy_unverified.
--
-- The projection itself is derived at read time from these facts (pure,
-- deterministic); no per-subject objective state table in this stage.

CREATE TABLE IF NOT EXISTS learning_objectives (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    title VARCHAR(255) NOT NULL,
    behavior TEXT NOT NULL,
    capability_type VARCHAR(32) NOT NULL DEFAULT 'concept',
    contract_type VARCHAR(32) NOT NULL DEFAULT 'concept_two_family',
    contract_params JSONB,
    contract_version VARCHAR(64) NOT NULL DEFAULT '',
    source_refs JSONB,
    content_version VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    prereq_objective_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_objectives_scope
    ON learning_objectives (tenant_id, knowledge_base_id, slug, status);

ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS objective_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS family_id VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS objective_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS family_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS content_version VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_learning_quiz_attempts_objective
    ON learning_quiz_attempts (tenant_id, subject_id, knowledge_base_id, objective_id);
