package adapters

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"os"
	"time"

	"github.com/np1-control-center/ncc/internal/h264tail"
)

const (
	cameraEncoderHeader = 56
	cameraEncoderChunk  = 60000
	cameraEncoderMaxAU  = 4 << 20
	cameraEncoderBuffer = 4 << 20
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
	parameterSets   []byte
	timingStarted   time.Time
	timingFrames    uint32
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
	// IDR access units can span several 60 KiB datagrams.  The stock encoder
	// must never block, so give the receiver as much queue as the kernel allows
	// and drain it independently from the client connection below.
	if err := conn.SetReadBuffer(cameraEncoderBuffer); err != nil {
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
		if frame.sequence == 0 {
			s.parameterSets = bytes.Clone(frame.data)
			if frame.fpsMilli >= 1000 && frame.fpsMilli <= 120000 {
				s.config, _ = h264tail.ConfigFromAnnexB(s.parameterSets, frame.fpsMilli)
			}
			continue
		}
		if len(s.config.AVC) == 0 && len(s.parameterSets) != 0 {
			fpsMilli := frame.fpsMilli
			if fpsMilli < 1000 || fpsMilli > 120000 {
				if s.timingStarted.IsZero() {
					s.timingStarted = time.Now()
				}
				s.timingFrames++
				if s.timingFrames >= 16 {
					elapsed := time.Since(s.timingStarted)
					if elapsed >= time.Second {
						fpsMilli = uint32((uint64(s.timingFrames-1)*uint64(time.Second)*1000 + uint64(elapsed)/2) / uint64(elapsed))
					}
				}
			}
			if fpsMilli >= 1000 && fpsMilli <= 120000 {
				if config, configErr := h264tail.ConfigFromAnnexB(s.parameterSets, fpsMilli); configErr == nil {
					s.config = config
					log.Printf("camera encoder parameters ready source=%d fpsMilli=%d width=%d height=%d", s.source, fpsMilli, config.Width, config.Height)
				}
			}
			if len(s.config.AVC) == 0 && s.received <= 3 {
				limit := len(frame.data)
				if limit > 64 {
					limit = 64
				}
				log.Printf("camera encoder timing pending source=%d sequence=%d nalType=%d pts=%d bytes=%d fpsMilli=%d width=%d height=%d prefix=%x", s.source, frame.sequence, frame.nalType, frame.pts, len(frame.data), frame.fpsMilli, frame.width, frame.height, frame.data[:limit])
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
