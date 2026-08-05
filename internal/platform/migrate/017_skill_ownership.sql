-- +goose Up
-- Ownership is resolved from patterns, never denormalised onto skills. A
-- cached owner column goes stale the moment a rule changes and the staleness
-- is invisible.
--
-- A per-skill owner list is a rule whose pattern is an exact name. One table,
-- one resolution algorithm, one UI list.
CREATE TABLE ownership_rules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern     TEXT NOT NULL UNIQUE,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ownership_rule_members (
    rule_id     UUID NOT NULL REFERENCES ownership_rules(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (rule_id, user_id)
);

CREATE INDEX idx_ownership_rule_members_user ON ownership_rule_members (user_id);

-- Who cleared what, and when. actor is the join; actor_email is a snapshot,
-- so deleting a user never erases the record of who approved a version. An
-- automatic release from a verified evaluation writes actor = NULL with
-- actor_email = 'system:eval', which is what keeps "released by a score" and
-- "released by a person" distinguishable forever.
--
-- UNIQUE (version_id, reason) makes each reason decided exactly once:
-- rejection is terminal and the way forward is a new version.
CREATE TABLE version_approvals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    version_id   UUID NOT NULL REFERENCES skill_versions(id) ON DELETE CASCADE,
    reason       TEXT NOT NULL,
    decision     TEXT NOT NULL,
    actor        UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_email  TEXT NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (version_id, reason)
);

ALTER TABLE version_approvals
    ADD CONSTRAINT version_approvals_reason_check
    CHECK (reason IN ('scan', 'ownership'));

ALTER TABLE version_approvals
    ADD CONSTRAINT version_approvals_decision_check
    CHECK (decision IN ('approved', 'rejected'));

CREATE INDEX idx_version_approvals_version ON version_approvals (version_id);

-- TEXT[], not JSONB: json.Marshal on a nil slice emits null, not [], and a
-- null in a column declared NOT NULL DEFAULT '[]' makes "no reasons" and
-- "never written" indistinguishable. A Postgres array has no such ambiguity.
ALTER TABLE skill_versions
    ADD COLUMN hold_reasons TEXT[] NOT NULL DEFAULT '{}';

-- Every version held today is held for a scan finding: that is the only
-- reason that has ever existed. Rejected and released rows keep '{}'.
UPDATE skill_versions SET hold_reasons = ARRAY['scan']
WHERE gate_state = 'needs_review';

-- +goose Down
ALTER TABLE skill_versions DROP COLUMN IF EXISTS hold_reasons;
DROP TABLE IF EXISTS version_approvals;
DROP TABLE IF EXISTS ownership_rule_members;
DROP TABLE IF EXISTS ownership_rules;
