-- +goose Up
-- The ONE spelling of "the vote counts as cast" (DESIGN.md gap 8): a cast
-- vote is a stored individual_votes row whose vote is a positional value
-- (yes, no, abstain). Absent, dispensed, paired and other are not positions:
-- they stay stored for display and audit but never enter a Rice aggregate,
-- a loyalty modal, or a pairwise agreement count.
--
-- Before 004 the predicate sat in four hand-written IN-lists across the
-- analytics queries (cohesion, loyalty, and both pair-agreement arms);
-- every consumer now selects or joins THIS view, keyed exactly like
-- individual_votes (votation_id, representative_id, vote) so it composes
-- with rep_party_at_date (migration 003).
--
-- The schema CHECK (001) stays the boundary validation; this view is the
-- analytical membership rule, deliberately derived from the stored rows so
-- the two can never drift apart.
CREATE VIEW cast_votes AS
SELECT votation_id, representative_id, vote
FROM individual_votes
WHERE vote IN ('yes', 'no', 'abstain');

-- +goose Down
DROP VIEW IF EXISTS cast_votes;
