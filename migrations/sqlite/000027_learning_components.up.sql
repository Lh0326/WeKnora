CREATE TABLE IF NOT EXISTS learning_components (
 id TEXT PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 knowledge_base_id TEXT NOT NULL,
 version TEXT NOT NULL,
 definition TEXT NOT NULL,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_learning_components_scope ON learning_components(tenant_id, knowledge_base_id);
