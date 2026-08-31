-- Knowledge network & guided learning (Lite). Mirrors versioned/000085.
-- Row ids are generated in Go, so there is no server-side default here.

CREATE TABLE IF NOT EXISTS learning_events (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    weight REAL NOT NULL DEFAULT 0,
    session_id VARCHAR(36),
    message_id VARCHAR(36),
    occurred_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
    logit REAL NOT NULL DEFAULT 0,
    evidence_count INTEGER NOT NULL DEFAULT 0,
    positive_count INTEGER NOT NULL DEFAULT 0,
    negative_count INTEGER NOT NULL DEFAULT 0,
    stability REAL NOT NULL DEFAULT 0,
    last_evidence_at DATETIME,
    first_seen_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_states_scope
    ON mastery_states (tenant_id, subject_id, knowledge_base_id, slug);

CREATE TABLE IF NOT EXISTS memory_wiki_map (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    normalized_topic_key VARCHAR(255) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    topic_label VARCHAR(255) NOT NULL DEFAULT '',
    confidence REAL NOT NULL DEFAULT 0,
    decided_by VARCHAR(16) NOT NULL DEFAULT 'llm',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_wiki_map_scope
    ON memory_wiki_map (tenant_id, subject_id, knowledge_base_id, normalized_topic_key, slug);

CREATE TABLE IF NOT EXISTS learning_edges (
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    from_slug VARCHAR(512) NOT NULL,
    to_slug VARCHAR(512) NOT NULL,
    relation VARCHAR(16) NOT NULL DEFAULT 'prerequisite',
    confidence REAL NOT NULL DEFAULT 0,
    source VARCHAR(16) NOT NULL DEFAULT 'heuristic+llm',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_edges_scope
    ON learning_edges (tenant_id, knowledge_base_id, from_slug, to_slug);

CREATE TABLE IF NOT EXISTS learning_quiz_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    question TEXT NOT NULL,
    options TEXT,
    correct_key VARCHAR(4) NOT NULL,
    explanation TEXT,
    chunk_refs TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_quiz_items_scope
    ON learning_quiz_items (tenant_id, knowledge_base_id, slug, status);

CREATE TABLE IF NOT EXISTS learning_quiz_attempts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    quiz_item_id VARCHAR(36) NOT NULL,
    slug VARCHAR(512) NOT NULL,
    chosen_key VARCHAR(4) NOT NULL,
    is_correct BOOLEAN NOT NULL DEFAULT 0,
    answered_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_learning_quiz_attempts_scope
    ON learning_quiz_attempts (tenant_id, subject_id, knowledge_base_id, slug);
CREATE INDEX IF NOT EXISTS idx_learning_quiz_attempts_item
    ON learning_quiz_attempts (tenant_id, subject_id, quiz_item_id, answered_at);

CREATE TABLE IF NOT EXISTS learning_subject_prefs (
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    collect_disabled BOOLEAN NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_subject_prefs_scope
    ON learning_subject_prefs (tenant_id, subject_id);
