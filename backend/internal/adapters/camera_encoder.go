package adapters

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/np1-control-center/ncc/internal/h264tail"
)

const (
	cameraEncoderHeader = 56
	cameraEncoderChunk  = 60000
	cameraEncoderMaxAU  = 4 << 20
)

type cameraEncoderFrame struct {
	data                    []byte
	sequence, width, height uint32
	fpsMilli, nalType       uint32
	pts                     uint64
}

type cameraEncoderStream struct {
	conn            *net.UnixConn
	path            string
	source          uint32
	current         cameraEncoderFrame
	next            uint32
	dropped         uint64
	config          h264tail.Config
	locked          bool
	acceptedDropped uint64
	received        uint64
}

func cameraEncoderPath(source string) (string, uint32, error) {
	switch source {
	case "front":
		return "/run/np1-control/camera-encoder-front.sock", 1, nil
	case "in":
		return "/run/np1-control/camera-encoder-in.sock", 2, nil
	default:
		return "", 0, errors.New("invalid camera source")
	}
}

func openCameraEncoderStream(source string) (*cameraEncoderStream, error) {
	name, id, err := cameraEncoderPath(source)
	if err != nil {
		return nil, err
	}
	// The path is NCC-owned runtime state. A per-camera process mutex prevents
	// replacing a socket that is serving another viewer.
	_ = os.Remove(name)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(name, 0666); err != nil {
		conn.Close()
		_ = os.Remove(name)
		return nil, err
	}
	return &cameraEncoderStream{conn: conn, path: name, source: id}, nil
}

func (s *cameraEncoderStream) Close() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	_ = os.Remove(s.path)
}

func (s *cameraEncoderStream) nextFrame(ctx context.Context) (cameraEncoderFrame, error) {
	buffer := make([]byte, cameraEncoderHeader+cameraEncoderChunk)
	for {
		if err := ctx.Err(); err != nil {
			return cameraEncoderFrame{}, err
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := s.conn.ReadFromUnix(buffer)
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			continue
		}
		if err != nil {
			return cameraEncoderFrame{}, err
		}
		if n < cameraEncoderHeader || !bytes.Equal(buffer[:8], []byte("NCCAU001")) {
			s.dropped++
			continue
		}
		read32 := func(at int) uint32 { return binary.LittleEndian.Uint32(buffer[at : at+4]) }
		source, header, sequence := read32(8), read32(12), read32(16)
		total, offset, chunk := read32(20), read32(24), read32(28)
		if source != s.source || header != cameraEncoderHeader || total == 0 || total > cameraEncoderMaxAU || chunk == 0 || chunk > cameraEncoderChunk || offset > total || chunk > total-offset || n != cameraEncoderHeader+int(chunk) {
			s.dropped++
			continue
		}
		if offset == 0 {
			s.current = cameraEncoderFrame{data: make([]byte, 0, total), sequence: sequence, width: read32(32), height: read32(36), fpsMilli: read32(40), nalType: read32(44), pts: binary.LittleEndian.Uint64(buffer[48:56])}
			s.next = 0
		}
		if s.current.sequence != sequence || offset != s.next || uint32(cap(s.current.data)) != total {
			s.current = cameraEncoderFrame{}
			s.next = 0
			s.dropped++
			continue
		}
		s.current.data = append(s.current.data, buffer[cameraEncoderHeader:n]...)
		s.next += chunk
		if s.next == total {
			s.received++
			frame := s.current
			s.current = cameraEncoderFrame{}
			s.next = 0
			return frame, nil
		}
	}
}

func (s *cameraEncoderStream) Next(ctx context.Context) (h264tail.Sample, error) {
	for {
		frame, err := s.nextFrame(ctx)
		if err != nil {
			return h264tail.Sample{}, err
		}
		if frame.sequence == 0 || len(s.config.AVC) == 0 {
			if config, configErr := h264tail.ConfigFromAnnexB(frame.data, frame.fpsMilli); configErr == nil {
				s.config = config
			}
			if frame.sequence == 0 {
				continue
			}
		}
		if len(s.config.AVC) == 0 {
			continue
		}
		sample, err := s.config.SampleFromAnnexB(frame.data)
		if err != nil {
			s.dropped++
			continue
		}
		if s.dropped != s.acceptedDropped {
			s.locked = false
			s.acceptedDropped = s.dropped
		}
		if sample.Keyframe {
			s.locked = true
		} else if !s.locked {
			continue
		}
		return sample, nil
	}
}

// serveCameraEncoderH264 returns an error only before committing an HTTP
// response. Once the first validated IDR is available, all output is sourced
// directly from the stock encoder's access-unit stream.
func serveCameraEncoderH264(w http.ResponseWriter, r *http.Request, source string) error {
	stream, err := openCameraEncoderStream(source)
	if err != nil {
		return err
	}
	defer stream.Close()
	start, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var sample h264tail.Sample
	for !sample.Keyframe {
		sample, err = stream.Next(start)
		if err != nil {
			log.Printf("camera encoder unavailable source=%s frames=%d configured=%t dropped=%d: %v", source, stream.received, len(stream.config.AVC) != 0, stream.dropped, err)
			return err
		}
	}
	init, err := stream.config.InitSegment()
	if err != nil {
		return err
	}
	codec, err := stream.config.Codec()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Camera-Codec", codec)
	w.Header().Set("X-Camera-Width", strconv.Itoa(int(stream.config.Width)))
	w.Header().Set("X-Camera-Height", strconv.Itoa(int(stream.config.Height)))
	w.Header().Set("X-Camera-FPS", fmt.Sprintf("%.3f", float64(stream.config.Timescale)/float64(stream.config.SampleDuration)))
	w.Header().Set("X-Camera-Audio", "none")
	w.Header().Set("X-Camera-Source", "encoder")
	controller := http.NewResponseController(w)
	write := func(data []byte) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err := w.Write(data); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := write(init); err != nil {
		return nil
	}
	sequence, dts := uint32(1), uint64(0)
	for {
		fragment, err := stream.config.Fragment(sequence, dts, []h264tail.Sample{sample})
		if err != nil || write(fragment) != nil {
			return nil
		}
		sequence++
		dts += uint64(stream.config.SampleDuration)
		// DriveRec can suspend its encoder when the vehicle/bench state no
		// longer requests ordinary recording.  End this response after a short
		// idle period so the browser can reconnect as soon as output resumes.
		next, nextCancel := context.WithTimeout(r.Context(), 4*time.Second)
		sample, err = stream.Next(next)
		nextCancel()
		if err != nil {
			return nil
		}
	}
}
