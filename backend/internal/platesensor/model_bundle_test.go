package platesensor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The configured field-head label and decoder files must match the hashes in both configuration and manifest.
func TestFieldHeadModelBundle(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "models", "plate")
	read := func(name string, v any) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	type model struct {
		File string `json:"file"`
		SHA  string `json:"sha256"`
	}
	var cfg struct {
		OCR struct {
			Text struct {
				Heads struct {
					LabelsFile     string  `json:"labelsFile"`
					LabelsSHA      string  `json:"labelsSha256"`
					ClassThreshold float64 `json:"classThreshold"`
					KanaThreshold  float64 `json:"kanaThreshold"`
					SharedInput    bool    `json:"sharedInput"`
					ClassDecoder   model   `json:"classDecoder"`
					KanaDecoder    model   `json:"kanaDecoder"`
				} `json:"fieldHeads"`
			} `json:"textOcr"`
		} `json:"ocr"`
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	read("plate-model.json", &cfg)
	read("model-manifest.json", &manifest)
	h := cfg.OCR.Text.Heads
	if h.ClassThreshold != .7 || h.KanaThreshold != .7 {
		t.Fatal("unexpected field head thresholds")
	}
	if !h.SharedInput {
		t.Fatal("field heads must share the text OCR pass")
	}
	for name, expected := range map[string]string{h.LabelsFile: h.LabelsSHA, h.ClassDecoder.File: h.ClassDecoder.SHA, h.KanaDecoder.File: h.KanaDecoder.SHA} {
		if name == "" || filepath.Base(name) != name {
			t.Fatalf("invalid model filename %q", name)
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		actual := hex.EncodeToString(sum[:])
		if actual != expected || manifest.Files[name] != actual {
			t.Errorf("hash mismatch for %s", name)
		}
	}
}
