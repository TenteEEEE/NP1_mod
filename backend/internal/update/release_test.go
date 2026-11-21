package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

func testManifest() manifest {
	digest := strings.Repeat("a", 64)
	return manifest{Schema: 1, Product: "np1cc", Target: "linux/arm64", Version: "0.5.1", MinUpdater: 1,
		From: []string{digest}, DriveRec: digest, Hook: digest, Injector: digest,
		Asset: assetName, AssetSize: 10, AssetSHA: digest, BinarySize: 10, BinarySHA: digest}
}

func TestSignedManifestAndLimits(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	m := testManifest()
	raw, _ := json.Marshal(m)
	sig := ed25519.Sign(private, raw)
	if got, err := verifyManifest(raw, sig, public); err != nil || !reflect.DeepEqual(got, m) {
		t.Fatalf("manifest=%+v, err=%v", got, err)
	}
	if _, err := verifyManifest(append(raw, ' '), sig, public); err == nil {
		t.Fatal("accepted modified signed data")
	}
	m.BinarySize = maxBinary + 1
	raw, _ = json.Marshal(m)
	if _, err := verifyManifest(raw, ed25519.Sign(private, raw), public); err == nil {
		t.Fatal("accepted oversized signed binary")
	}
}

func TestReleasesFilterAndCompareVersionsUsingSelectedNetwork(t *testing.T) {
	fetch := func(_ context.Context, wifiOnly bool, address string, _ int64, out io.Writer) error {
		if !wifiOnly || address != "https://api.github.com/repos/owner/project/releases?per_page=30" {
			t.Fatalf("unexpected routing: %t %s", wifiOnly, address)
		}
		_, err := io.WriteString(out, `[{"tag_name":"v0.5.2"},{"tag_name":"v0.5.10"},{"tag_name":"v0.5.1"},{"tag_name":"v0.5.0"},{"tag_name":"plate-models-v2"},{"tag_name":"v9.0.0","draft":true},{"tag_name":"v9.1.0","prerelease":true}]`)
		return err
	}
	got, err := releases(context.Background(), fetch, "owner/project", true, "0.5.1")
	if err != nil || len(got) != 2 || got[0].Tag != "v0.5.10" || got[1].Tag != "v0.5.2" {
		t.Fatalf("releases=%+v, err=%v", got, err)
	}
	for _, address := range []string{"http://github.com/x", "https://github.com.evil/x", "https://github.com@evil/x", "https://github.com:443/x"} {
		if validURL(address, true) {
			t.Errorf("accepted %q", address)
		}
	}
	delivery := "https://release-assets.githubusercontent.com/file?token=value"
	if validURL(delivery, false) || !validURL(delivery, true) {
		t.Fatal("release delivery URL should only be accepted as a redirect")
	}
}
