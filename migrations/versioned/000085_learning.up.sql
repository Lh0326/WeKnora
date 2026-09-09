-- Migration 000085: knowledge network & guided learning (topic 4).
--
-- Seven plain relational tables, no vector columns, no PostgreSQL-specific
-- types beyond JSONB. learning_events is the append-only fact source; every
-- other subject-scoped table is a fold, projection or record that can be
-- rebuilt from it.
--
-- Identity is (tenant_id, subject_id, knowledge_base_id, slug): the wiki
-- page slug (with its type prefix) is the node identity, and slug drift is
-- repaired by periodic alias reconciliation rather than by hooks inside the
-- wiki write path. mastery_states deliberately stores no level and no
-- decayed probability — both are read-time derivations, so thresholds and
-- stability formulas can be retuned without rewriting history.
--
-- Scope convention follows the memory subsystem: subject_id is
-- Principal.StorageID(), rows belong to one workspace, and the KB-shared
-- tables (learning_edges, learning_quiz_items) carry no subject dimension
-- because they are personal-data-free inventory.

CREATE TABLE IF NOT EXISTS learning_events (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    -- Wiki page slug with type prefix, e.g. "concept/rag".
    slug VARCHAR(512) NOT NULL,
    -- answer_cite | re_ask | cross_ref | topic_signal | quiz_correct |
    -- quiz_wrong | quiz_unsure | wiki_tool_read | backfill_cite |
    -- self_assess_up | self_assess_down_all | self_assess_down_doc_gap |
    -- self_assess_down_doc_updated | self_assess_down_quiz_easy
    event_type VARCHAR(32) NOT NULL,
    -- Frozen at append time so replaying history reproduces the state that
    -- was visible at the time, even after constants are retuned.
    weight DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- Trace back to the conversation that produced the event.
    session_id VARCHAR(36),
    message_id VARCHAR(36),
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_events_scope
    ON learning_events (tenant_id, subject_id, knowledge_base_id, slug, occurred_at);
CREATE INDEX IF NOT EXISTS idx_learning_events_subject_time
    ON learning_events (tenant_id, subject_id, occurred_at);

CREATE TABLE IF NOT EXISTS mastery_states (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    -- Clamped running logit; p = sigmoid(logit).
    logit DOUBLE PRECISION NOT NULL DEFAULT 0,
    evidence_count INTEGER NOT NULL DEFAULT 0,
    positive_count INTEGER NOT NULL DEFAULT 0,
    negative_count INTEGER NOT NULL DEFAULT 0,
    -- Stability in days, grown only by positive evidence (FSRS-inspired).
    stability DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_evidence_at TIMESTAMP WITH TIME ZONE,
    first_seen_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- One row per (person, node): the fold target every event append upserts.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_states_scope
    ON mastery_states (tenant_id, subject_id, knowledge_base_id, slug);

CREATE TABLE IF NOT EXISTS memory_wiki_map (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    -- memory_topic_stats' normalised topic key; mapping at topic (not item)
    -- granularity rides the stable identity items are normalised under.
    normalized_topic_key VARCHAR(255) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    topic_label VARCHAR(255) NOT NULL DEFAULT '',
    -- Below the confidence floor the adjudicator's answer is not stored.
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- llm | manual
    decided_by VARCHAR(16) NOT NULL DEFAULT 'llm',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_wiki_map_scope
    ON memory_wiki_map (tenant_id, subject_id, knowledge_base_id, normalized_topic_key, slug);

CREATE TABLE IF NOT EXISTS learning_edges (
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    -- "learn `to_slug` after mastering `from_slug`".
    from_slug VARCHAR(512) NOT NULL,
    to_slug VARCHAR(512) NOT NULL,
    -- Always 'prerequisite' today; related/none are adjudicated away.
    relation VARCHAR(16) NOT NULL DEFAULT 'prerequisite',
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- heuristic+llm | manual
    source VARCHAR(16) NOT NULL DEFAULT 'heuristic+llm',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_edges_scope
    ON learning_edges (tenant_id, knowledge_base_id, from_slug, to_slug);

CREATE TABLE IF NOT EXISTS learning_quiz_items (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    question TEXT NOT NULL,
    -- Four options keyed "A".."D".
    options JSONB,
    correct_key VARCHAR(4) NOT NULL,
    explanation TEXT,
    -- Grounding contract: must be a subset of the page's ChunkRefs.
    chunk_refs JSONB,
    -- active | disabled (human review can disable, never hard-delete).
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_quiz_items_scope
    ON learning_quiz_items (tenant_id, knowledge_base_id, slug, status);

CREATE TABLE IF NOT EXISTS learning_quiz_attempts (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    quiz_item_id VARCHAR(36) NOT NULL,
    -- Denormalised from the item so deletes and per-node stats need no join.
    slug VARCHAR(512) NOT NULL,
    chosen_key VARCHAR(4) NOT NULL,
    is_correct BOOLEAN NOT NULL DEFAULT false,
    answered_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_quiz_attempts_scope
    ON learning_quiz_attempts (tenant_id, subject_id, knowledge_base_id, slug);
CREATE INDEX IF NOT EXISTS idx_learning_quiz_attempts_item
    ON learning_quiz_attempts (tenant_id, subject_id, quiz_item_id, answered_at);

CREATE TABLE IF NOT EXISTS learning_subject_prefs (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    -- The collection opt-out; profile delete may set it so a deleted
    -- profile cannot resurrect on the next question.
    collect_disabled BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_subject_prefs_scope
    ON learning_subject_prefs (tenant_id, subject_id);
