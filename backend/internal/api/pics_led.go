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

func picsLED(client *http.Client) func(context.Context, led.TrafficColors, time.Time, time.Time) error {
	return func(ctx context.Context, colors led.TrafficColors, deadline, visitUntil time.Time) error {
		ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		body := strings.NewReader(fmt.Sprintf(`{"colors":%d,"expiresAtMs":%d,"visitUntilMs":%d}`, colors, deadline.UnixMilli(), visitUntil.UnixMilli()))
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

func (p *picsService) trafficColors(now time.Time) (led.TrafficColors, time.Time, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.settings.Enabled || !p.settings.LED || p.scanState != "scanning" {
		return 0, time.Time{}, time.Time{}
	}
	var colors led.TrafficColors
	var deadline time.Time
	for _, signal := range p.monitor.FreshSignals(now) {
		if !p.directionAllows(signal.ID, signal.Slot, now) {
			continue
		}
		if deadline.IsZero() || signal.FreshUntil.Before(deadline) {
			deadline = signal.FreshUntil
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
	visitUntil := p.monitor.VisitUntil(now, func(id string, slot int) bool {
		return p.directionAllows(id, slot, now)
	})
	return colors, deadline, visitUntil
}

func (p *picsService) runLED(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var active bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			colors, deadline, visitUntil := p.trafficColors(time.Now())
			if next := !visitUntil.IsZero(); (next || active) && p.setLED(ctx, colors, deadline, visitUntil) == nil {
				active = next
			}
		}
	}
}
