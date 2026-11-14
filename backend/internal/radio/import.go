package radio

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/np1-control-center/ncc/internal/trips"
)

// ReadRecordedPositions recovers measured coordinates from an earlier, private
// /recordings/track export. It does not infer metre accuracy from HDOP or invent
// positions for timestamps absent from that export.
func ReadRecordedPositions(reader io.Reader, now time.Time) ([]Position, bool, error) {
	const limit = 32 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	var value struct {
		Status string            `json:"status"`
		Data   trips.TrackReport `json:"data"`
	}
	if len(raw) > limit || json.Unmarshal(raw, &value) != nil || value.Status != "available" || value.Data.Source != "stock-recording-nmea" || value.Data.Points == nil || len(value.Data.Points) > trips.MaximumIndexedSamples {
		return nil, false, errors.New("invalid measured recording track export")
	}
	if _, err = trips.TrackDate(value.Data.Date); err != nil {
		return nil, false, err
	}
	positions := make([]Position, 0, len(value.Data.Points))
	for _, p := range value.Data.Points {
		position := Position{At: p.At, Lat: p.Lat, Lon: p.Lon, AccuracyM: p.AccuracyM, HDOP: p.HDOP, Source: value.Data.Source}
		if p.At.After(now) || p.At.In(trips.Japan).Format("2006-01-02") != value.Data.Date || !validPosition(Observation{At: p.At, GPS: &position}) {
			return nil, false, errors.New("invalid or out-of-day position in recording export")
		}
		positions = append(positions, position)
	}
	return positions, value.Data.SourceTruncated, nil
}
