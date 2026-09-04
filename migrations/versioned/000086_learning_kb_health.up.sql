-- Migration 000086: knowledge-health org aggregate (topic 4).
--
-- The owner/admin health view reads EVERY subject's mastery rows of one KB
-- in a single query. The unique index from 000085 (tenant, subject, kb,
-- slug) cannot serve a subject-less predicate — knowledge_base_id sits
-- behind subject_id in its column order — so this index gives the
-- (tenant, kb) scan its own leading-column range.
--
-- learning_events deliberately gains no new index here: the maintenance
-- marks read (tenant, kb, event_type IN self-assess kinds, occurred_at) is
-- one demo-scale scan per health view open, while learning_events carries
-- the highest write volume of the learning tables (every answer touch).
-- Revisit when real deployments report the read as hot.

CREATE INDEX IF NOT EXISTS idx_mastery_states_kb
    ON mastery_states (tenant_id, knowledge_base_id);
