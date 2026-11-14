package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/np1-control-center/ncc/internal/plate"
	"github.com/np1-control-center/ncc/internal/trips"
)

// Records that got no position at detection time are filled in from the recorder's GNSS log
// once the clip's NMEA sidecar is on the SD card (it is written when the clip closes, about
// every three minutes). Past records are filled in too while their sidecars remain.
const (
	plateBackfillEvery   = 2 * time.Minute
	plateBackfillDelay   = 4 * time.Minute
	plateBackfillDays    = 30
	plateBackfillRecords = 50 // two times per record, within the broker's limit of 100
	plateBackfillRetry   = 10 * time.Minute
	plateBackfillMaxWait = 6 * time.Hour
)

type plateBackfillState struct {
	next  time.Time
	tries int
}

// plateBrokerPositions asks the broker for the logged position nearest to each time.
func plateBrokerPositions(client *http.Client) func(context.Context, []time.Time) ([]*plate.Location, error) {
	return func(ctx context.Context, at []time.Time) ([]*plate.Location, error) {
		q := url.Values{}
		for _, t := range at {
			q.Add("at", t.UTC().Format(time.RFC3339Nano))
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://broker/recordings/positions?"+q.Encode(), nil)
		if e != nil {
			return nil, e
		}
		resp, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, errors.New("position lookup failed: " + resp.Status)
		}
		var body struct {
			Positions []*trips.Sample `json:"positions"`
		}
		if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); e != nil {
			return nil, e
		}
		if len(body.Positions) != len(at) {
			return nil, errors.New("position lookup returned a different count")
		}
		out := make([]*plate.Location, len(at))
		for i, s := range body.Positions {
			if s != nil {
				out[i] = &plate.Location{At: s.At, Lat: s.Lat, Lon: s.Lon, AccuracyM: s.AccuracyM}
			}
		}
		return out, nil
	}
}

// backfillLocations fills in positions for records without one and returns how many it
// filled. Each record is looked up at its first and last sighting; a record whose time has no
// logged fix is retried with a growing interval (the SD card may have been away).
func (p *plateService) backfillLocations(ctx context.Context, now time.Time) int {
	if p.positions == nil {
		return 0
	}
	records, _, _ := p.store.List(plate.Query{From: plate.Day(now.AddDate(0, 0, -(plateBackfillDays - 1))), To: plate.Day(now), Limit: plate.MaxListLimit})
	if p.backfill == nil {
		p.backfill = map[string]plateBackfillState{}
	}
	listed := map[string]bool{}
	due := []plate.Record{}
	for _, r := range records {
		if r.Location != nil {
			continue
		}
		listed[r.ID] = true
		if now.Sub(r.FirstSeen) < plateBackfillDelay || len(due) == plateBackfillRecords {
			continue
		}
		if st, ok := p.backfill[r.ID]; ok && now.Before(st.next) {
			continue
		}
		due = append(due, r)
	}
	for id := range p.backfill {
		if !listed[id] {
			delete(p.backfill, id)
		}
	}
	if len(due) == 0 {
		return 0
	}
	at := make([]time.Time, 0, 2*len(due))
	for _, r := range due {
		at = append(at, r.FirstSeen, r.LastSeen)
	}
	found, e := p.positions(ctx, at)
	if e != nil {
		return 0 // broker or SD card unavailable: the next round tries again
	}
	filled := 0
	for i, r := range due {
		loc := found[2*i]
		if loc == nil {
			loc = found[2*i+1]
		}
		if loc == nil {
			st := p.backfill[r.ID]
			st.tries++
			wait := plateBackfillRetry << min(st.tries-1, 6)
			st.next = now.Add(min(wait, plateBackfillMaxWait))
			p.backfill[r.ID] = st
			continue
		}
		if p.store.SetLocation(r.ID, r.FirstSeen, *loc) == nil {
			filled++
			delete(p.backfill, r.ID)
		}
	}
	return filled
}
