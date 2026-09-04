-- Knowledge-health org aggregate (Lite). Mirrors versioned/000086.
--
-- Same rationale as the PG migration: the (tenant, kb) org read needs a
-- leading-column index the 000012 unique index cannot serve, and
-- learning_events stays unindexed-for-this-read because demo-scale scans
-- do not justify an index on the learning layer's hottest write table.

CREATE INDEX IF NOT EXISTS idx_mastery_states_kb
    ON mastery_states (tenant_id, knowledge_base_id);
