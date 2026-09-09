-- Migration 000088: user-declared skip list (已掌握，不再推荐).
--
-- The self-assessment track ("up") lifts the score but pins the node to
-- the top of the recommendation queue until quiz proof lands — correct for
-- a claim the system wants verified, wrong for the "看一眼就知道已经会了"
-- case: the user's intent there is to REMOVE the node from the queue, not
-- to start a verification conversation. learning_skips is that separate
-- intent: a persistent, reversible per-subject declaration that says
-- "I consider myself done with this node; stop recommending it".
--
-- Deliberately its own table, not a learning_events row: events fold into
-- mastery and expire from view windows, while a skip is a standing
-- preference that must hold until the user revokes it — closer to the
-- opt-out prefs than to evidence. It is personal data, so profile delete
-- and the KB orphan sweep remove it.

CREATE TABLE IF NOT EXISTS learning_skips (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    -- Wiki page slug with type prefix, e.g. "concept/rag".
    slug VARCHAR(512) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_skips_scope
    ON learning_skips (tenant_id, subject_id, knowledge_base_id, slug);
CREATE INDEX IF NOT EXISTS idx_learning_skips_scope_subject
    ON learning_skips (subject_id);
