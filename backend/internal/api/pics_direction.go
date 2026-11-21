package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/np1-control-center/ncc/internal/config"
	"github.com/np1-control-center/ncc/internal/trips"
)

const picsMaxIntersections = 500

type picsAxes [6]*float64

func validPICSID(id string) bool {
	_, err := hex.DecodeString(id)
	return len(id) == 8 && id == strings.ToUpper(id) && err == nil
}

func validPICSAxes(axes picsAxes) bool {
	for _, axis := range axes {
		if axis != nil && (*axis < 0 || *axis >= 360 || math.IsNaN(*axis)) {
			return false
		}
	}
	return true
}

func loadPICSDirections(path string) map[string]picsAxes {
	rules := map[string]picsAxes{}
	if data, err := os.ReadFile(path); err == nil && len(data) <= 256*1024 {
		var saved map[string]picsAxes
		if json.Unmarshal(data, &saved) == nil && len(saved) <= picsMaxIntersections {
			for id, axes := range saved {
				if validPICSID(id) && validPICSAxes(axes) {
					rules[id] = axes
				}
			}
		}
	}
	return rules
}

func (s *Server) picsDirectionRules(w http.ResponseWriter, r *http.Request) {
	s.pics.mu.Lock()
	rules := maps.Clone(s.pics.directions)
	s.pics.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	jsonOut(w, 200, rules)
}

func (s *Server) savePICSDirectionRule(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Axes []*float64 `json:"axes"`
	}
	if !decode(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var axes picsAxes
	if len(input.Axes) != len(axes) || !validPICSID(id) {
		fail(w, 400, "INVALID_PICS_DIRECTION", "交差点と6灯分の方向を確認してください")
		return
	}
	copy(axes[:], input.Axes)
	if !validPICSAxes(axes) {
		fail(w, 400, "INVALID_PICS_DIRECTION", "横断方向は0度以上360度未満で指定してください")
		return
	}
	p := s.pics
	p.mu.Lock()
	defer p.mu.Unlock()
	rules := maps.Clone(p.directions)
	if axes == (picsAxes{}) {
		delete(rules, id)
	} else {
		rules[id] = axes
	}
	if len(rules) > picsMaxIntersections {
		fail(w, 409, "PICS_DIRECTION_LIMIT", "横断方向の登録上限に達しています")
		return
	}
	if err := config.Atomic(p.directionsPath, rules); err != nil {
		fail(w, 503, "PICS_DIRECTION_SAVE_FAILED", "横断方向を保存できません")
		return
	}
	p.directions = rules
	p.cancelInvalidCue(time.Now())
	jsonOut(w, 200, axes)
}

func (p *picsService) currentPICSCourse(now time.Time) *float64 {
	if n := len(p.positions); n > 0 && p.positions[n-1].At.Sub(now).Abs() <= 5*time.Second {
		return p.course
	}
	return nil
}

func (p *picsService) directionAllows(id string, slot int, now time.Time) bool {
	if !p.settings.Directional {
		return true
	}
	axis := p.directions[id][slot-1]
	course := p.currentPICSCourse(now)
	if axis == nil || course == nil {
		return false
	}
	difference := math.Mod(math.Abs(*course-*axis), 180)
	return math.Min(difference, 180-difference) <= 25
}

func picsGPS(client *http.Client) func(context.Context) *trips.TrackPoint {
	return func(ctx context.Context) *trips.TrackPoint {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://broker/pics/gps", nil)
		if err != nil {
			return nil
		}
		response, err := client.Do(request)
		if err != nil {
			return nil
		}
		defer response.Body.Close()
		var result struct {
			Data struct {
				trips.TrackPoint
				Fix bool `json:"fix"`
			} `json:"data"`
		}
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&result) != nil {
			return nil
		}
		fix := result.Data.TrackPoint
		if !result.Data.Fix || fix.At.Sub(time.Now()).Abs() > 5*time.Second || !trips.Usable(trips.Sample{At: fix.At, Lat: fix.Lat, Lon: fix.Lon, Valid: true, Quality: 1}) {
			return nil
		}
		return &fix
	}
}

func (p *picsService) collectPICSGPS(ctx context.Context) {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			p.mu.Lock()
			collect := p.settings.Enabled && p.scanState == "scanning"
			p.mu.Unlock()
			if collect {
				if fix := p.gps(ctx); fix != nil {
					p.mu.Lock()
					if p.settings.Enabled && p.scanState == "scanning" {
						p.addPICSPosition(*fix)
					}
					p.mu.Unlock()
				}
			}
		}
	}
}

func (p *picsService) addPICSPosition(fix trips.TrackPoint) {
	if n := len(p.positions); n > 0 {
		gap := fix.At.Sub(p.positions[n-1].At)
		if gap <= 0 {
			return
		}
		if gap > trips.MaximumGap {
			p.positions = p.positions[:0]
		}
	}
	for len(p.positions) > 0 && fix.At.Sub(p.positions[0].At) > trips.CourseHistory+15*time.Second {
		p.positions = p.positions[1:]
	}
	p.positions = append(p.positions, fix)
	p.course = trips.CourseBefore(p.positions, fix.At)
}
