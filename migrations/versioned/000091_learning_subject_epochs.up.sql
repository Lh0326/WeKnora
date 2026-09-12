-- Migration 000091: deletion-epoch fence for profile delete serialization.
--
-- Review finding P1-A: the personal-data sweep, the collection opt-out and
-- the background-write fence were three separate writes. A crash between
-- them could leave a deleted profile whose opt-out was never recorded, and
-- a topic-mapping adjudication that returned after the delete could land
-- its result and resurrect the profile — the opt-out switch alone does not
-- cover "delete, then re-enable collection".
--
-- One row per subject, bumped inside the same transaction that deletes the
-- profile (repository.DeleteProfileData). Background writers capture the
-- epoch before their model call and re-check it inside the write
-- transaction (repository.ApplyTopicMapping): a stale epoch means the
-- profile was deleted mid-flight and the write is discarded.
--
-- Subject-scoped, not tenant-scoped, for the same reason as
-- learning_subject_prefs: shared-KB rows land under the KB owner's
-- effective tenant while deletion is a property of the person.

CREATE TABLE IF NOT EXISTS learning_subject_epochs (
    subject_id VARCHAR(512) PRIMARY KEY,
    -- Counts profile deletions; row absent = epoch 0.
    epoch BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
