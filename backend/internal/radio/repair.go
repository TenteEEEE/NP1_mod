package radio

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type RepairReport struct {
	Records           int  `json:"records"`
	AlreadyLocated    int  `json:"alreadyLocated"`
	Matched           int  `json:"matched"`
	Unmatched         int  `json:"unmatched"`
	Updated           int  `json:"updated"`
	FilesChanged      int  `json:"filesChanged"`
	Applied           bool `json:"applied"`
	PositionCount     int  `json:"positionCount,omitempty"`
	SourceTruncated   bool `json:"sourceTruncated"`
	ImportedPositions int  `json:"importedPositions,omitempty"`
}

// RepairPositions joins delayed fixes without changing radio values/times.
// It only retains changed lines in memory; unchanged files are never rewritten.
func (h *History) RepairPositions(positions []Position, apply bool) (RepairReport, error) {
	out := RepairReport{Applied: apply}
	valid := make([]Position, 0, len(positions))
	for _, p := range positions {
		if p.Source != "" && validPosition(Observation{At: p.At, GPS: &p}) {
			valid = append(valid, p)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].At.Before(valid[j].At) })
	if h.directory == "" {
		return out, errors.New("history directory unavailable")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, suffix := range []string{".1", ""} {
		filename := filepath.Join(h.directory, "radio-observations.jsonl"+suffix)
		f, err := os.Open(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return out, err
		}
		if info.Size() > maximumHistoryReadBytes {
			f.Close()
			return out, errors.New("radio history exceeds limit")
		}
		changes := map[int][]byte{}
		scanner := historyLines(f)
		outputSize := info.Size()
		for lineNumber := 0; scanner.Scan(); lineNumber++ {
			line := scanner.Bytes()
			var o Observation
			if json.Unmarshal(line, &o) != nil || o.At.IsZero() {
				continue
			}
			out.Records++
			if validPosition(o) && o.GPS.Source != "stock-image-recognition" {
				out.AlreadyLocated++
				continue
			}
			index := sort.Search(len(valid), func(j int) bool { return !valid[j].At.Before(o.At) })
			var nearest *Position
			for _, j := range []int{index - 1, index} {
				if j < 0 || j >= len(valid) {
					continue
				}
				p := &valid[j]
				if p.At.Sub(o.At).Abs() <= 5*time.Second && (nearest == nil || p.At.Sub(o.At).Abs() < nearest.At.Sub(o.At).Abs()) {
					nearest = p
				}
			}
			if nearest == nil {
				out.Unmatched++
				continue
			}
			out.Matched++
			if !apply {
				continue
			}
			var object map[string]json.RawMessage
			if err = json.Unmarshal(line, &object); err != nil {
				break
			}
			object["gps"], err = json.Marshal(nearest)
			if err != nil {
				break
			}
			var updated []byte
			updated, err = json.Marshal(object)
			if err != nil {
				break
			}
			if bytes.HasSuffix(line, []byte("\r\n")) {
				updated = append(updated, '\r', '\n')
			} else if bytes.HasSuffix(line, []byte("\n")) {
				updated = append(updated, '\n')
			}
			changes[lineNumber] = updated
			outputSize += int64(len(updated) - len(line))
		}
		scanErr := scanner.Err()
		if err == nil {
			err = scanErr
		}
		if err == nil && outputSize > maximumHistoryReadBytes {
			err = errors.New("repaired history exceeds read limit")
		}
		if err == nil && len(changes) > 0 {
			_, err = f.Seek(0, io.SeekStart)
			if err == nil {
				err = replaceHistoryLines(filename, f, changes)
			}
			if err == nil {
				out.Updated += len(changes)
				out.FilesChanged++
			}
		}
		f.Close()
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Include line endings in the token so unchanged and malformed lines survive
// byte-for-byte, including a final record without a newline.
func historyLines(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(io.LimitReader(reader, maximumHistoryReadBytes+1))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	return scanner
}

func replaceHistoryLines(filename string, source io.Reader, changes map[int][]byte) error {
	f, err := os.CreateTemp(filepath.Dir(filename), ".radio-repair-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	hash := sha256.New()
	writer := bufio.NewWriter(io.MultiWriter(f, hash))
	scanner := historyLines(source)
	for line := 0; scanner.Scan(); line++ {
		raw := scanner.Bytes()
		if updated, ok := changes[line]; ok {
			raw = updated
		}
		if _, err = writer.Write(raw); err != nil {
			break
		}
	}
	if err == nil {
		err = scanner.Err()
	}
	if err == nil {
		err = writer.Flush()
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	// Close the original before rename for Windows test/workstation compatibility.
	if closer, ok := source.(io.Closer); ok {
		if err = closer.Close(); err != nil {
			return err
		}
	}
	if err = os.Rename(name, filename); err != nil {
		return err
	}
	readback, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer readback.Close()
	actual := sha256.New()
	if _, err = io.Copy(actual, readback); err != nil {
		return err
	}
	if !bytes.Equal(hash.Sum(nil), actual.Sum(nil)) {
		return errors.New("radio repair read-back mismatch")
	}
	return nil
}
