package build

import (
	"archive/zip"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unburger/internal/locres"
)

func writeFixturePayload(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	blob, err := locres.Write(locres.File{Version: 3, Entries: []locres.Entry{
		{Namespace: "ST_Test_01", Key: "Key", Text: "Text", SourceHash: 1, NamespaceHash: 0x11, KeyHash: 0x22},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Game.locres"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readArtifactMeta(t *testing.T, zipPath string, pc bool) ([]byte, []string) {
	t.Helper()
	if pc {
		raw, err := os.ReadFile(strings.TrimSuffix(zipPath, ".zip") + ".mod-meta.json")
		if err != nil {
			t.Fatal(err)
		}
		return raw, nil
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	var meta []byte
	for _, f := range r.File {
		names = append(names, f.Name)
		if f.Name == "mod-meta.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			meta, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(meta) == 0 {
		t.Fatal("mod-meta.json is missing")
	}
	return meta, names
}

// TestPackageMetadataIsNeutralAndComplete pins the generated metadata schema for both platform cases: only the
// documented keys, the required values, 40-character SHA-1 fields, runtime UNVERIFIED, and no private path in
// the archive.
func TestPackageMetadataIsNeutralAndComplete(t *testing.T) {
	payload := writeFixturePayload(t)
	dist := t.TempDir()
	const version = "0.1.0"
	cases := []Target{
		{Platform: PlatformSwitch, Layout: LayoutAtmosphere, Edition: EditionCanonical, Ready: true, State: StatePass},
		{Platform: PlatformPC, Layout: LayoutPC, Edition: EditionCanonical, Ready: true, State: StatePass},
	}
	allowedKeys := map[string]bool{
		"artifact":         true,
		"version":          true,
		"platform":         true,
		"layout":           true,
		"edition":          true,
		"title_id":         true,
		"format":           true,
		"resource":         true,
		"pak":              true,
		"pak_sha1":         true,
		"pak_payload_sha1": true,
		"runtime":          true,
		"mount_root":       true,
		"generated_by":     true,
	}
	for _, tg := range cases {
		path, err := PackageLayout(payload, dist, tg, version)
		if err != nil {
			t.Fatalf("%s: %v", tg.Platform, err)
		}
		raw, names := readArtifactMeta(t, path, tg.Platform == PlatformPC)
		var meta map[string]interface{}
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatalf("%s: %v", tg.Platform, err)
		}
		for key := range meta {
			if !allowedKeys[key] {
				t.Fatalf("%s: undocumented metadata key %q", tg.Platform, key)
			}
		}
		if _, ok := meta["translation_by"]; ok {
			t.Fatalf("%s: metadata must not carry an attribution field", tg.Platform)
		}
		want := map[string]string{
			"artifact":     ArtifactName(tg, version),
			"version":      version,
			"platform":     tg.Platform,
			"layout":       tg.Layout,
			"edition":      tg.Edition,
			"title_id":     TitleID,
			"format":       "layeredfs-pak-v11",
			"resource":     LocresVirtualPath,
			"pak":          PatchPakName(version),
			"runtime":      RuntimeUnknown,
			"mount_root":   mountRootFor(tg, modRoot(tg)),
			"generated_by": "unburger build all",
		}
		for key, value := range want {
			got, ok := meta[key].(string)
			if !ok {
				t.Fatalf("%s: metadata %s = %v, want string %q", tg.Platform, key, meta[key], value)
			}
			if got != value {
				t.Fatalf("%s: metadata %s = %q, want %q", tg.Platform, key, got, value)
			}
		}
		for _, key := range []string{"pak_sha1", "pak_payload_sha1"} {
			sum, ok := meta[key].(string)
			if !ok || len(sum) != 40 {
				t.Fatalf("%s: %s = %v, want 40 hex characters", tg.Platform, key, meta[key])
			}
			if _, err := hex.DecodeString(sum); err != nil {
				t.Fatalf("%s: %s = %q, want hexadecimal: %v", tg.Platform, key, sum, err)
			}
		}
		for _, name := range names {
			if strings.HasPrefix(name, ".local/") || strings.Contains(name, "/.local/") {
				t.Fatalf("%s: public archive carries a private path %q", tg.Platform, name)
			}
		}
	}
}

// TestStructuralPackageDoesNotClaimRuntimeApproval keeps the two states apart: a package may be structurally
// complete, readable and packaged while its runtime status remains UNVERIFIED.
func TestStructuralPackageDoesNotClaimRuntimeApproval(t *testing.T) {
	payload := writeFixturePayload(t)
	path, err := PackageLayout(payload, t.TempDir(), Target{
		Platform: PlatformSwitch, Layout: LayoutEmulator, Edition: EditionCanonical,
		Ready: true, State: StatePass,
	}, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := readArtifactMeta(t, path, false)
	var meta map[string]interface{}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["runtime"] != RuntimeUnknown {
		t.Fatalf("structural package claimed runtime %v", meta["runtime"])
	}
}
