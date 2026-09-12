CREATE TABLE learning_plan_preferences (
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    revision VARCHAR(36) NOT NULL,
    limit_to_folder BOOLEAN NOT NULL DEFAULT FALSE,
    folder_id VARCHAR(128) NOT NULL DEFAULT '',
    depth VARCHAR(16) NOT NULL,
    time_budget_minutes INTEGER NOT NULL CHECK (time_budget_minutes BETWEEN 1 AND 120),
    use_memory BOOLEAN NOT NULL,
    goal_objectives JSONB NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, subject_id, knowledge_base_id)
);
CREATE INDEX idx_learning_plan_preferences_subject ON learning_plan_preferences(subject_id);
