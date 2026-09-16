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
