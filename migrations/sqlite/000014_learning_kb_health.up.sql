-- Index supporting knowledge-base scoped cleanup and learning-state reads.

CREATE INDEX IF NOT EXISTS idx_mastery_states_kb
    ON mastery_states (tenant_id, knowledge_base_id);
