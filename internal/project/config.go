package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Status struct {
	SourceVerified     bool `json:"source_verified"`
	AdapterImplemented bool `json:"adapter_implemented"`
	SafeToPatch        bool `json:"safe_to_patch"`
	BuildPass          bool `json:"build_pass"`
	RuntimePass        bool `json:"runtime_pass"`
	ReleaseApproved    bool `json:"release_approved"`
}
type Platform struct {
	Enabled        bool     `json:"enabled"`
	GameID         string   `json:"game_id,omitempty"`
	PreferredPaths []string `json:"preferred_paths"`
}
type Audio struct {
	PreferredVoiceLanguage string `json:"preferred_voice_language"`
	TextLanguage           string `json:"text_language"`
	VoicePatchVerified     bool   `json:"voice_patch_verified"`
}
type Config struct {
	SchemaVersion     int                 `json:"schema_version"`
	ProjectID         string              `json:"project_id"`
	Title             string              `json:"title"`
	SourceLanguage    string              `json:"source_language"`
	TargetLanguage    string              `json:"target_language"`
	CanonicalPlatform string              `json:"canonical_platform"`
	SteamAppID        string              `json:"steam_app_id"`
	Platforms         map[string]Platform `json:"platforms"`
	Audio             Audio               `json:"audio"`
	Status            Status              `json:"status"`
}

var ErrNotConfigured = errors.New("source and resource writer are not independently verified")

func Load(root string) (Config, error) {
	var c Config
	paths := []string{filepath.Join(root, ".local", "config", "project.json"), filepath.Join(root, "config", "project.example.json")}
	var b []byte
	var err error
	for _, p := range paths {
		b, err = os.ReadFile(p)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return c, err
		}
	}
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid configuration: %w", err)
	}
	if c.SchemaVersion != 2 || c.SourceLanguage != "ja" || c.TargetLanguage != "en" || c.CanonicalPlatform != "switch" {
		return c, errors.New("unexpected project schema or language configuration")
	}
	if !c.Platforms["switch"].Enabled || !c.Platforms["pc"].Enabled {
		return c, errors.New("both targets must be enabled")
	}
	for _, p := range c.Platforms {
		for _, s := range p.PreferredPaths {
			if s == "" || filepath.IsAbs(s) || strings.HasPrefix(filepath.Clean(s), "..") {
				return c, fmt.Errorf("unsafe relative source path %q", s)
			}
		}
	}
	return c, nil
}
func (c Config) ReadyForBuild() error {
	if !c.Status.SourceVerified || !c.Status.AdapterImplemented || !c.Status.SafeToPatch || !c.Status.RuntimePass {
		return ErrNotConfigured
	}
	return nil
}
