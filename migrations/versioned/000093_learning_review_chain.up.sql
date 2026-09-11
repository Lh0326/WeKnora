-- Migration 000093: strict verification item lifecycle, review audit and
-- structured application tasks (stage 2).
--
-- Quiz items gain the review-chain columns: content/rubric/scorer
-- versions, assistance mode, family fingerprint (clone detection), and the
-- human review audit (reviewer, published_at, note, change kind). Quiz
-- attempts freeze the trial conditions and the server's eligibility
-- verdict. learning_tasks + learning_task_attempts add the contract-B
-- structured application path: form fields with an exact-match critical
-- check list, answer key server-only.

ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS content_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS rubric_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS scorer_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS assistance_mode VARCHAR(16) NOT NULL DEFAULT 'closed_book';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS family_fingerprint VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS reviewer VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS review_note TEXT;
ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS change_kind VARCHAR(16) NOT NULL DEFAULT '';

ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS assistance_mode VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS item_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS eligible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE learning_quiz_attempts
    ADD COLUMN IF NOT EXISTS grade_reason VARCHAR(32) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS learning_tasks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    objective_id VARCHAR(36) NOT NULL DEFAULT '',
    family_id VARCHAR(64) NOT NULL DEFAULT '',
    title VARCHAR(255) NOT NULL,
    scenario TEXT NOT NULL,
    fields JSONB,
    answer_key JSONB,
    critical_checks JSONB,
    source_refs JSONB,
    content_version VARCHAR(64) NOT NULL DEFAULT '',
    rubric_version VARCHAR(64) NOT NULL DEFAULT '',
    scorer_version VARCHAR(64) NOT NULL DEFAULT '',
    assistance_mode VARCHAR(16) NOT NULL DEFAULT 'open_book',
    reviewer VARCHAR(512) NOT NULL DEFAULT '',
    published_at TIMESTAMP WITH TIME ZONE,
    review_note TEXT,
    change_kind VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_tasks_scope
    ON learning_tasks (tenant_id, knowledge_base_id, slug, status);

CREATE TABLE IF NOT EXISTS learning_task_attempts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    task_id VARCHAR(36) NOT NULL,
    objective_id VARCHAR(36) NOT NULL DEFAULT '',
    family_id VARCHAR(64) NOT NULL DEFAULT '',
    slug VARCHAR(512) NOT NULL,
    answers JSONB,
    checks JSONB,
    is_passed BOOLEAN NOT NULL DEFAULT FALSE,
    assistance_mode VARCHAR(16) NOT NULL DEFAULT '',
    task_status VARCHAR(16) NOT NULL DEFAULT '',
    content_version VARCHAR(64) NOT NULL DEFAULT '',
    eligible BOOLEAN NOT NULL DEFAULT FALSE,
    grade_reason VARCHAR(32) NOT NULL DEFAULT '',
    submitted_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_task_attempts_scope
    ON learning_task_attempts (tenant_id, subject_id, knowledge_base_id, objective_id);
CREATE INDEX IF NOT EXISTS idx_learning_task_attempts_task
    ON learning_task_attempts (task_id, submitted_at);
