-- +goose Up
-- The ONE spelling of "which party did this deputy hold on the vote date?"
-- (DESIGN.md §8): membership spans meet each votation on its date-only
-- vote_date. Before 003, this predicate was hand-written in six sites across
-- four query files in four formulations (direct joins, two EXISTS, scalar
-- subqueries with a special-cased start bound); every consumer now selects
-- or joins THIS view, keyed by (votation_id, representative_id) — the shape
-- all of them join on.
--
-- Bounds are inclusive on both sides: end_date exactly == vote_date still
-- covers (attribute the final day's vote to the party being left).
--
-- The schema deliberately keeps "at most one party per representative at any
-- date" OUT of the DB (001's invariant comment); seed-file validation
-- enforces it — the view only reads the curated spans, one row per
-- (votation, representative, membership) pair.
--
-- The '0000-00-00' unparseable-vote_date sentinel can never match a real
-- membership bound, so an unparseable date renders as unaffiliated —
-- surfaced, never misattributed (the old scalar-subquery formulation
-- special-cased the start bound to the same effect; one rule now).
CREATE VIEW rep_party_at_date AS
SELECT vt.id                AS votation_id,
       rp.representative_id AS representative_id,
       rp.party_id          AS party_id
FROM representative_parties rp
JOIN votations vt
  ON rp.start_date <= vt.vote_date
 AND (rp.end_date IS NULL OR rp.end_date >= vt.vote_date);

-- +goose Down
DROP VIEW IF EXISTS rep_party_at_date;
