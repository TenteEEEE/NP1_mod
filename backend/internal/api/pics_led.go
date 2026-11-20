package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/led"
	"github.com/np1-control-center/ncc/internal/pics"
)

func picsLED(client *http.Client) func(context.Context, led.TrafficColors) error {
	return func(ctx context.Context, colors led.TrafficColors) error {
		ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		body := strings.NewReader(fmt.Sprintf(`{"colors":%d}`, colors))
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://broker/led/traffic", body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			return fmt.Errorf("traffic LED broker status %d", res.StatusCode)
		}
		return nil
	}
}

func (p *picsService) trafficColors(now time.Time) led.TrafficColors {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.settings.Enabled || !p.settings.LED || p.scanState != "scanning" {
		return 0
	}
	var colors led.TrafficColors
	for _, signal := range p.monitor.FreshSignals(now) {
		if !p.directionAllows(signal.ID, signal.Slot, now) {
			continue
		}
		switch signal.Color {
		case pics.Red:
			colors |= led.TrafficRed
		case pics.BlinkGreen:
			colors |= led.TrafficBlinkGreen
		case pics.Green:
			colors |= led.TrafficGreen
		}
	}
	return colors
}

func (p *picsService) runLED(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var previous led.TrafficColors
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			colors := p.trafficColors(time.Now())
			if (colors != 0 || previous != 0) && p.setLED(ctx, colors) == nil {
				previous = colors
			}
		}
	}
}
