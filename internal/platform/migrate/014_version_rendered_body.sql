-- +goose Up
-- A held version's rendered prose has nowhere to live. The gate deliberately
-- keeps description/content/frontmatter off the skills row while a version is
-- held, but until now those values existed only in the request that published
-- it: releasing the version later advanced the pointer and left the served
-- prose stale forever, and the spec-derived columns (tags, author, license,
-- compatibility, spec_compliance) were never written at all, which makes the
-- skill invisible to tag filtering.
--
-- Storing the rendered body on the version row makes release a pure database
-- operation — no archive re-read, no re-parse — and makes "what would this
-- version serve" answerable for a version that is not being served.
ALTER TABLE skill_versions
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN content     TEXT NOT NULL DEFAULT '';

-- Backfill the currently-served version of each skill from the skills row.
-- That is the one pairing that is known to be correct: skills.description and
-- skills.content were last written by whichever version latest_version points
-- at. Older versions keep the empty default; nothing reads them, because only
-- a release can make a version served and only versions published from here on
-- can be held.
UPDATE skill_versions sv
SET description = s.description, content = s.content
FROM skills s
WHERE s.id = sv.skill_id AND s.latest_version = sv.version;

-- +goose Down
ALTER TABLE skill_versions
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS content;
