-- name: GetPartyVoteCounts :many
-- Per-party vote counts for one votation (party-at-date join). Consumed by
-- the breakdown chart and, in Phase 2, the Rice-cohesion arithmetic.
SELECT p.id AS party_id, p.short_name, p.name AS party_name, p.color,
       iv.vote AS vote, COUNT(*) AS n
FROM individual_votes iv
JOIN representatives r ON r.id = iv.representative_id
JOIN votations v ON v.id = iv.votation_id
JOIN representative_parties rp
    ON rp.representative_id = r.id
   AND rp.start_date <= v.vote_date
   AND (rp.end_date IS NULL OR rp.end_date >= v.vote_date)
JOIN parties p ON p.id = rp.party_id
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
JOIN representative_parties rp
  ON rp.representative_id = iv.representative_id
 AND rp.start_date <= v.vote_date
 AND (rp.end_date IS NULL OR rp.end_date >= v.vote_date)
JOIN parties p ON p.id = rp.party_id
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
JOIN representative_parties rp
  ON rp.representative_id = iv.representative_id
 AND rp.start_date <= v.vote_date
 AND (rp.end_date IS NULL OR rp.end_date >= v.vote_date)
JOIN parties p ON p.id = rp.party_id
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
        SELECT 1 FROM representative_parties rpa JOIN parties pa ON pa.id = rpa.party_id
        WHERE rpa.representative_id = a.representative_id AND pa.id = CAST(sqlc.arg(party_id) AS INTEGER)
          AND rpa.start_date <= v.vote_date
          AND (rpa.end_date IS NULL OR rpa.end_date >= v.vote_date)))
  AND (CAST(sqlc.arg(party_id) AS INTEGER) = 0 OR EXISTS (
        SELECT 1 FROM representative_parties rpb JOIN parties pb ON pb.id = rpb.party_id
        WHERE rpb.representative_id = b.representative_id AND pb.id = CAST(sqlc.arg(party_id) AS INTEGER)
          AND rpb.start_date <= v.vote_date
          AND (rpb.end_date IS NULL OR rpb.end_date >= v.vote_date)))
GROUP BY a.representative_id, b.representative_id;
