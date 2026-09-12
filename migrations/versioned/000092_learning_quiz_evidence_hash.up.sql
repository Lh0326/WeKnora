-- Migration 000092: quiz items bind their evidence version.
--
-- Review finding P2-D: questions had no content version. A regenerated wiki
-- page (or an edited chunk) could leave old questions serving and scoring
-- against material that no longer says what the question asserts — the
-- answer key silently drifts from the evidence.
--
-- evidence_hash freezes a digest of the page content plus the cited chunk
-- contents at generation time. The maintenance pass recomputes the digest;
-- active items whose hash no longer matches go to status 'stale' (serving
-- and scoring stop) and the deficit-driven regeneration rebuilds them from
-- the current material. Legacy rows carry '' ("version unknown") and are
-- staled on the first pass able to compute a digest — conservative
-- correctness over serving continuity.

ALTER TABLE learning_quiz_items
    ADD COLUMN IF NOT EXISTS evidence_hash VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_learning_quiz_items_evidence
    ON learning_quiz_items (tenant_id, knowledge_base_id, slug, status, evidence_hash);
