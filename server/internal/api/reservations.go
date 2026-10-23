package api

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// A machine that drafts from its cached next state (see docs/drafting-latency.md) holds a reservation on the member
// it will announce next, one per roster, so another machine can't draft that member first. Reservations are made
// and extended by every response that carries the next state, expire after reservationTTL, and go when their member
// is drafted. Other machines' reservations count as busy for a member's name (copy 1).

const reservationTTL = 24 * time.Hour

var machineIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// machineID is the caller's machine from the X-Rostr-Machine header, or "" when it sends none.
func machineID(r *http.Request) (string, error) {
	id := r.Header.Get("X-Rostr-Machine")
	if id != "" && !machineIDPattern.MatchString(id) {
		return "", invalidRequest("X-Rostr-Machine must be 1 to 64 letters, digits, underscores or hyphens.")
	}
	return id, nil
}

// reservation is one machine's hold on a member of a roster.
type reservation struct {
	rosterID, machineID, memberName string
}

// reservations is every unexpired reservation on the owner's rosters.
func reservations(ctx context.Context, q querier, ownerID string) ([]reservation, error) {
	var all []reservation
	batch := &pgx.Batch{}
	queueReservations(batch, ownerID, &all)
	err := q.SendBatch(ctx, batch).Close()
	return all, err
}

// queueReservations queues reservations on a batch, filling all once the batch runs.
func queueReservations(batch *pgx.Batch, ownerID string, all *[]reservation) {
	batch.Queue(`
		select v.roster_id, v.machine_id, v.member_name from reservations v join rosters r on r.id = v.roster_id
		where r.owner_id = $1 and v.expires_at > now()`, ownerID).Query(func(rows pgx.Rows) error {
		var err error
		*all, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (reservation, error) {
			var v reservation
			err := row.Scan(&v.rosterID, &v.machineID, &v.memberName)
			return v, err
		})
		return err
	})
}

// reservedByOthers marks the names other machines hold in a roster as busy, so a draft from this machine skips them.
func reservedByOthers(all []reservation, rosterID, machine string, busy map[slot]bool) {
	for _, v := range all {
		if v.rosterID == rosterID && v.machineID != machine {
			busy[slot{nameKey(v.memberName), 1}] = true
		}
	}
}

// ownReservation is the member this machine holds in a roster, or "".
func ownReservation(all []reservation, rosterID, machine string) string {
	for _, v := range all {
		if v.rosterID == rosterID && v.machineID == machine {
			return v.memberName
		}
	}
	return ""
}

// reserveNext gives the machine a reservation in each of the owner's rosters on the member a draft would take next
// (a free name, never a numbered copy), recomputed every time so a cast edit or a release moves it. Without a
// machine nothing is reserved.
func reserveNext(ctx context.Context, tx pgx.Tx, ownerID, machine string) error {
	if machine == "" {
		return nil
	}
	rows, err := tx.Query(ctx, `select `+rosterColumns+` from rosters r where r.owner_id = $1`, ownerID)
	if err != nil {
		return err
	}
	rosters, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (rosterRow, error) { return scanRoster(row) })
	if err != nil {
		return err
	}
	all, err := reservations(ctx, tx, ownerID)
	if err != nil {
		return err
	}
	for _, roster := range rosters {
		busy, err := busySlots(ctx, tx, roster.ID)
		if err != nil {
			return err
		}
		reservedByOthers(all, roster.ID, machine, busy)
		last, err := lastDrafted(ctx, tx, roster.ID)
		if err != nil {
			return err
		}
		member := ""
		if m, copy := freeMember(roster, busy, last); copy == 1 {
			member = m.Name
		}
		if member == "" {
			_, err = tx.Exec(ctx, `delete from reservations where roster_id = $1 and machine_id = $2`, roster.ID, machine)
		} else {
			_, err = tx.Exec(ctx, `
				insert into reservations (roster_id, machine_id, member_name, expires_at) values ($1, $2, $3, now() + $4)
				on conflict (roster_id, machine_id) do update set member_name = excluded.member_name, expires_at = excluded.expires_at`,
				roster.ID, machine, member, reservationTTL)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// releaseReservations drops every machine's reservation on a member whose name was just drafted.
func releaseReservations(ctx context.Context, tx pgx.Tx, rosterID, memberName string) error {
	_, err := tx.Exec(ctx, `delete from reservations where roster_id = $1 and member_name = $2`, rosterID, memberName)
	return err
}
