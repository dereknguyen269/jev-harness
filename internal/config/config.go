package config

import (
	"os"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"gopkg.in/yaml.v3"
)

// Profile names a bundled policy posture.
type Profile string

const (
	ProfileDefault    Profile = "default"
	ProfileStrict     Profile = "strict"
	ProfileDeveloper  Profile = "developer"
	ProfilePermissive Profile = "permissive"
)

// Config is the gateway configuration.
type Config struct {
	Listen     string
	PolicyPath string
	Profile    Profile

	JevEndpoint string
	JevAPIKey   string
	JevModel    string

	Thresholds  domain.ConfidenceThresholds
	ApprovalTTL int // seconds
	AuditPath   string
}

// Default returns gateway defaults.
func Default() Config {
	return Config{
		Listen:      "127.0.0.1:8787",
		PolicyPath:  "configs/policy.yaml",
		Profile:     ProfileDefault,
		Thresholds:  domain.DefaultThresholds(),
		ApprovalTTL: 30,
	}
}

// ProfileFile maps a profile to its bundled YAML.
func ProfileFile(p Profile) string {
	switch p {
	case ProfileStrict:
		return "configs/profiles/strict.yaml"
	case ProfileDeveloper:
		return "configs/profiles/developer.yaml"
	case ProfilePermissive:
		return "configs/profiles/permissive.yaml"
	default:
		return "configs/profiles/default.yaml"
	}
}

// LoadProfile reads a profile YAML (policies: format) — missing file is OK.
func LoadProfile(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out map[string]any
	if err := yaml.NewDecoder(f).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
