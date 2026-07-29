-- +goose Up
-- Agents report activations with different semantics: Claude Code and OpenCode
-- report an explicit tool invocation, Cursor scans a transcript at session end
-- and dedupes per session. Summing them unlabelled overstates comparability.
ALTER TABLE skill_events
    ADD COLUMN event_source TEXT NOT NULL DEFAULT 'tool_invocation';

ALTER TABLE skill_events
    ADD CONSTRAINT skill_events_event_source_check
    CHECK (event_source IN ('tool_invocation', 'transcript_scan'));

-- Cursor's only reporter has always been the transcript-scanning stop hook.
UPDATE skill_events SET event_source = 'transcript_scan' WHERE agent = 'cursor';

CREATE INDEX idx_events_skill_source ON skill_events (skill_name, event_source);

-- +goose Down
DROP INDEX IF EXISTS idx_events_skill_source;
ALTER TABLE skill_events DROP CONSTRAINT skill_events_event_source_check;
ALTER TABLE skill_events DROP COLUMN event_source;
