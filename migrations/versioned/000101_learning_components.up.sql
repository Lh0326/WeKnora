CREATE TABLE IF NOT EXISTS learning_components (
 id VARCHAR(36) PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 knowledge_base_id VARCHAR(36) NOT NULL,
 version VARCHAR(64) NOT NULL,
 definition JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_learning_components_scope ON learning_components(tenant_id, knowledge_base_id);
