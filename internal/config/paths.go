package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths are the three locations of spec 4.2. Config is the config.toml file; State and Data are directories.
type Paths struct {
	Config string
	State  string
	Data   string
}

// ResolvePaths applies the XDG defaults and the SAPULIDI_CONFIG, SAPULIDI_STATE and SAPULIDI_DATA overrides.
// SAPULIDI_CONFIG may name the file or a directory that holds config.toml. Every path must come out absolute.
func ResolvePaths() (Paths, error) {
	home, _ := os.UserHomeDir()
	pick := func(override, xdgVar, homeRel string) string {
		if v := os.Getenv(override); v != "" {
			return v
		}
		if v := os.Getenv(xdgVar); v != "" {
			return filepath.Join(v, "sapulidi")
		}
		if home == "" {
			return ""
		}
		return filepath.Join(home, homeRel, "sapulidi")
	}
	p := Paths{
		Config: pick("SAPULIDI_CONFIG", "XDG_CONFIG_HOME", ".config"),
		State:  pick("SAPULIDI_STATE", "XDG_STATE_HOME", ".local/state"),
		Data:   pick("SAPULIDI_DATA", "XDG_DATA_HOME", ".local/share"),
	}
	for name, v := range map[string]string{"config": p.Config, "state": p.State, "data": p.Data} {
		if !filepath.IsAbs(v) {
			return Paths{}, fmt.Errorf("config: %s path %q is not absolute", name, v)
		}
	}
	p.Config = filepath.Clean(p.Config)
	p.State = filepath.Clean(p.State)
	p.Data = filepath.Clean(p.Data)
	if fi, err := os.Stat(p.Config); (err == nil && fi.IsDir()) || filepath.Ext(p.Config) != ".toml" {
		p.Config = filepath.Join(p.Config, "config.toml")
	}
	return p, nil
}
