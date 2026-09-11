-- Strict verification item lifecycle and structured tasks (Lite). Mirrors versioned/000093.

ALTER TABLE learning_quiz_items ADD COLUMN content_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN rubric_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN scorer_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN assistance_mode VARCHAR(16) NOT NULL DEFAULT 'closed_book';
ALTER TABLE learning_quiz_items ADD COLUMN family_fingerprint VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN reviewer VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN published_at DATETIME;
ALTER TABLE learning_quiz_items ADD COLUMN review_note TEXT;
ALTER TABLE learning_quiz_items ADD COLUMN change_kind VARCHAR(16) NOT NULL DEFAULT '';

ALTER TABLE learning_quiz_attempts ADD COLUMN assistance_mode VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN item_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN eligible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE learning_quiz_attempts ADD COLUMN grade_reason VARCHAR(32) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS learning_tasks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    objective_id VARCHAR(36) NOT NULL DEFAULT '',
    family_id VARCHAR(64) NOT NULL DEFAULT '',
    title VARCHAR(255) NOT NULL,
    scenario TEXT NOT NULL,
    fields TEXT,
    answer_key TEXT,
    critical_checks TEXT,
    source_refs TEXT,
    content_version VARCHAR(64) NOT NULL DEFAULT '',
    rubric_version VARCHAR(64) NOT NULL DEFAULT '',
    scorer_version VARCHAR(64) NOT NULL DEFAULT '',
    assistance_mode VARCHAR(16) NOT NULL DEFAULT 'open_book',
    reviewer VARCHAR(512) NOT NULL DEFAULT '',
    published_at DATETIME,
    review_note TEXT,
    change_kind VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
    answers TEXT,
    checks TEXT,
    is_passed BOOLEAN NOT NULL DEFAULT FALSE,
    assistance_mode VARCHAR(16) NOT NULL DEFAULT '',
    task_status VARCHAR(16) NOT NULL DEFAULT '',
    content_version VARCHAR(64) NOT NULL DEFAULT '',
    eligible BOOLEAN NOT NULL DEFAULT FALSE,
    grade_reason VARCHAR(32) NOT NULL DEFAULT '',
    submitted_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_task_attempts_scope
    ON learning_task_attempts (tenant_id, subject_id, knowledge_base_id, objective_id);
CREATE INDEX IF NOT EXISTS idx_learning_task_attempts_task
    ON learning_task_attempts (task_id, submitted_at);
