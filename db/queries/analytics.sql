-- Party-at-date analytics: every query here joins rep_party_at_date (the
-- view from migration 003), the ONE spelling of the party-at-date rule.

-- name: GetPartyVoteCounts :many
-- Per-party vote counts for one votation via rep_party_at_date. Consumed by
-- the breakdown chart and, in Phase 2, the Rice-cohesion arithmetic.
SELECT p.id AS party_id, p.short_name, p.name AS party_name, p.color,
       iv.vote AS vote, COUNT(*) AS n
FROM individual_votes iv
JOIN rep_party_at_date pad
  ON pad.votation_id = iv.votation_id
 AND pad.representative_id = iv.representative_id
JOIN parties p ON p.id = pad.party_id
WHERE iv.votation_id = ?
GROUP BY p.id, iv.vote;

-- name: GetCohesionRows :many
-- Per-(party, votation) CAST-vote counts in a chamber + [from,to] vote_date
-- window (empty string = open bound), optional party filter (0 = all).
-- Cast votes only (yes/no/abstain): absent/dispensed/paired are not
-- positions and are excluded from every Rice aggregate (DESIGN.md gap 8).
SELECT p.id AS party_id, p.short_name, iv.votation_id, v.vote_date, iv.vote, COUNT(*) AS n
FROM individual_votes iv
JOIN votations v ON v.id = iv.votation_id
JOIN rep_party_at_date pad
  ON pad.votation_id = iv.votation_id
 AND pad.representative_id = iv.representative_id
JOIN parties p ON p.id = pad.party_id
WHERE v.chamber_id = sqlc.arg(chamber)
  AND iv.vote IN ('yes', 'no', 'abstain')
  AND (CAST(sqlc.arg(from_date) AS TEXT) = '' OR v.vote_date >= CAST(sqlc.arg(from_date) AS TEXT))
  AND (CAST(sqlc.arg(to_date) AS TEXT) = '' OR v.vote_date <= CAST(sqlc.arg(to_date) AS TEXT))
  AND (CAST(sqlc.arg(party_id) AS INTEGER) = 0 OR p.id = CAST(sqlc.arg(party_id) AS INTEGER))
GROUP BY p.id, iv.votation_id, iv.vote
ORDER BY v.vote_date;

-- name: GetLoyaltyRows :many
-- Rep-level cast votes with party-at-date in the window. Feeds the
-- loyalty/'rebels' arithmetic: per (party, votation) modal vote across ALL
-- party members, then each rep's vote compared against it.
SELECT iv.representative_id AS rep_id,
       r.first_name, r.last_name, r.second_last_name,
       p.id AS party_id, p.short_name AS party_short,
       iv.votation_id, v.vote_date, iv.vote
FROM individual_votes iv
JOIN votations v ON v.id = iv.votation_id
JOIN representatives r ON r.id = iv.representative_id
JOIN rep_party_at_date pad
  ON pad.votation_id = iv.votation_id
 AND pad.representative_id = iv.representative_id
JOIN parties p ON p.id = pad.party_id
WHERE v.chamber_id = sqlc.arg(chamber)
  AND iv.vote IN ('yes', 'no', 'abstain')
  AND (CAST(sqlc.arg(from_date) AS TEXT) = '' OR v.vote_date >= CAST(sqlc.arg(from_date) AS TEXT))
  AND (CAST(sqlc.arg(to_date) AS TEXT) = '' OR v.vote_date <= CAST(sqlc.arg(to_date) AS TEXT));

-- name: GetPairAgreementRows :many
-- Pairwise same-cast-vote counts for the alignment matrix: deputy pairs that
-- were both present (cast) on the same votation, how often they agreed.
-- Optional party filter applies party-at-date to BOTH pair members.
SELECT a.representative_id AS a_id, b.representative_id AS b_id,
       COUNT(*) AS joint,
       SUM(CASE WHEN a.vote = b.vote THEN 1 ELSE 0 END) AS same
FROM individual_votes a
JOIN individual_votes b
  ON b.votation_id = a.votation_id AND a.representative_id < b.representative_id
JOIN votations v ON v.id = a.votation_id
WHERE v.chamber_id = sqlc.arg(chamber)
  AND a.vote IN ('yes', 'no', 'abstain')
  AND b.vote IN ('yes', 'no', 'abstain')
  AND (CAST(sqlc.arg(from_date) AS TEXT) = '' OR v.vote_date >= CAST(sqlc.arg(from_date) AS TEXT))
  AND (CAST(sqlc.arg(to_date) AS TEXT) = '' OR v.vote_date <= CAST(sqlc.arg(to_date) AS TEXT))
  AND (CAST(sqlc.arg(party_id) AS INTEGER) = 0 OR EXISTS (
        SELECT 1 FROM rep_party_at_date pad
        WHERE pad.representative_id = a.representative_id
          AND pad.votation_id = a.votation_id
          AND pad.party_id = CAST(sqlc.arg(party_id) AS INTEGER)))
  AND (CAST(sqlc.arg(party_id) AS INTEGER) = 0 OR EXISTS (
        SELECT 1 FROM rep_party_at_date pad
        WHERE pad.representative_id = b.representative_id
          AND pad.votation_id = b.votation_id
          AND pad.party_id = CAST(sqlc.arg(party_id) AS INTEGER)))
GROUP BY a.representative_id, b.representative_id;
