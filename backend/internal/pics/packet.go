package pics

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
)

const Manufacturer = 0x01ce

type Color string

const (
	Red        Color = "red"
	BlinkGreen Color = "blink-green"
	Green      Color = "green"
)

type Lamp struct {
	Color Color `json:"color"`
	Step  int   `json:"-"`
}

type packet struct {
	id, name string
	kind     byte
	lamps    [6]Lamp
	position *Coordinates
}

type Coordinates struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Manufacturer Data offsets follow ble-pics-viewer (MIT, Tsubasa Yumura).
// The company ID is the ManufacturerData map key, outside this payload.
func parse(payload []byte) (packet, bool) {
	if len(payload) < 10 {
		return packet{}, false
	}
	p := packet{kind: payload[2], id: strings.ToUpper(hex.EncodeToString(payload[6:10]))}
	switch p.kind {
	case 0:
		if len(payload) < 24 {
			return packet{}, false
		}
		p.name = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r < ' ' || r == 0x7f {
				return -1
			}
			return r
		}, strings.ToValidUTF8(string(payload[10:24]), "")))
	case 1:
		if len(payload) < 18 {
			return packet{}, false
		}
		lat := float64(int32(binary.BigEndian.Uint32(payload[10:14]))) / 1e6
		lon := float64(int32(binary.BigEndian.Uint32(payload[14:18]))) / 1e6
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 || lat == 0 && lon == 0 {
			return packet{}, false
		}
		p.position = &Coordinates{Lat: lat, Lon: lon}
	case 2:
		if len(payload) < 11 {
			return packet{}, false
		}
		for i, value := range payload[10:min(len(payload), 16)] {
			switch value & 15 {
			case 1:
				p.lamps[i].Color = Red
			case 2:
				p.lamps[i].Color = BlinkGreen
			case 3:
				p.lamps[i].Color = Green
			}
			if value>>4 < 8 {
				p.lamps[i].Step = int(value>>4) + 1
			}
		}
	default:
		return packet{}, false
	}
	return p, true
}
