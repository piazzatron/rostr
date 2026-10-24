package api

import "context"

// liveCounts is what the live Worker gets: active drafts and idle members across every owner.
type liveCounts struct {
	Working int `json:"working"`
	Benched int `json:"benched"`
}

// queryLiveCounts counts active drafts and idle members across every owner. A member is idle when no active draft
// holds their name as copy 1, as in GET /v1/stats.
func queryLiveCounts(ctx context.Context, db querier) (liveCounts, error) {
	var counts liveCounts
	err := db.QueryRow(ctx, `
		with active as (
		  select d.roster_id, d.member_name, d.copy from drafts d where d.benched_at is null
		)
		select
		  (select count(*) from active),
		  (select count(*) from rosters r cross join jsonb_array_elements(r.a_team || r.b_team) m
		   where not exists (
		     select 1 from active a
		     where a.roster_id = r.id and a.copy = 1 and lower(a.member_name) = lower(m->>'name')))`,
	).Scan(&counts.Working, &counts.Benched)
	return counts, err
}
