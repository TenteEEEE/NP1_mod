package led

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
)

const trafficFramePath = "/run/np1-control/led-traffic.json"
const trafficLease = 750 * time.Millisecond

type TrafficColors uint8

const (
	TrafficRed TrafficColors = 1 << iota
	TrafficBlinkGreen
	TrafficGreen
)

func (c TrafficColors) Valid() bool { return c&^(TrafficRed|TrafficBlinkGreen|TrafficGreen) == 0 }

type trafficState struct {
	Colors    TrafficColors `json:"colors"`
	ExpiresAt time.Time     `json:"expiresAt"`
}

func SetTrafficColors(colors TrafficColors) error {
	return writeTrafficColors(trafficFramePath, colors, time.Now())
}

func writeTrafficColors(path string, colors TrafficColors, now time.Time) error {
	if !colors.Valid() {
		return errors.New("invalid traffic signal colors")
	}
	if err := screenPath(path, colors != 0); err != nil {
		return err
	}
	if colors == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return config.Atomic(path, trafficState{colors, now.Add(trafficLease).UTC()})
}

func readTrafficFrame(path string, now time.Time) (ScreenFrame, bool, error) {
	var frame ScreenFrame
	if err := screenPath(path, false); err != nil {
		return frame, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return frame, false, nil
	}
	if err != nil {
		return frame, false, err
	}
	var state trafficState
	if err = json.Unmarshal(data, &state); err != nil {
		return frame, false, err
	}
	if state.Colors == 0 || !state.Colors.Valid() || !now.Before(state.ExpiresAt) || state.ExpiresAt.Sub(now) > trafficLease {
		return frame, false, nil
	}
	frame.ExpiresAt = state.ExpiresAt
	frame.Sequence = uint64(state.Colors) << 1
	green := [3]int{0, 112, 84}
	if state.Colors&TrafficRed != 0 {
		frame.Zones[0], frame.Zones[1] = [3]int{112, 0, 0}, [3]int{112, 0, 0}
	}
	if state.Colors&TrafficBlinkGreen != 0 && now.UnixMilli()/500%2 == 0 {
		frame.Zones[2] = green
		frame.Sequence++
	}
	if state.Colors&TrafficGreen != 0 {
		frame.Zones[3], frame.Zones[4] = green, green
	}
	return frame, true, nil
}
