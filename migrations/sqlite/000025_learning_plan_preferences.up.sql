CREATE TABLE learning_plan_preferences (
    tenant_id INTEGER NOT NULL,
    subject_id TEXT NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    limit_to_folder INTEGER NOT NULL DEFAULT 0,
    folder_id TEXT NOT NULL DEFAULT '',
    depth TEXT NOT NULL,
    time_budget_minutes INTEGER NOT NULL CHECK (time_budget_minutes BETWEEN 1 AND 120),
    use_memory INTEGER NOT NULL,
    goal_objectives TEXT NOT NULL DEFAULT '[]',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, subject_id, knowledge_base_id)
);
CREATE INDEX idx_learning_plan_preferences_subject ON learning_plan_preferences(subject_id);
