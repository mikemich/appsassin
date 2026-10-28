// Package config loads and saves ~/.config/appsassin/config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Stealth controls the silent background sweeper.
type Stealth struct {
	Interval  string `json:"interval"`  // time between sweeps, e.g. "5m"
	Threshold int    `json:"threshold"` // minimum stray score to kill
	Apps      bool   `json:"apps"`      // also quit windowless, unused GUI apps
	AppIdle   string `json:"appIdle"`   // how long an app must go unused first
	DryRun    bool   `json:"dryRun"`    // log only, never kill
	Notify    bool   `json:"notify"`    // post a notification after each sweep that killed something
}

// Runaway decides when a process counts as stuck: burning CPU or swelling in
// memory for a sustained stretch, whether or not its parent is alive.
type Runaway struct {
	CPU    float64  `json:"cpu"`    // % of one core; 0 turns the CPU check off
	For    string   `json:"for"`    // how long it must stay over the line, e.g. "10m"
	Memory string   `json:"memory"` // resident size that counts when still growing, e.g. "4G"; "" turns it off
	Kill   bool     `json:"kill"`   // let stealth mode kill runaways
	Ignore []string `json:"ignore"` // never runaways, nor anything they started (builds, encodes)
}

type Config struct {
	Threshold int      `json:"threshold"` // minimum score shown in the TUI
	Minimal   bool     `json:"minimal"`   // start the TUI in minimal mode
	Ignore    []string `json:"ignore"`    // names, bundle IDs, exe paths or globs never touched
	Stealth   Stealth  `json:"stealth"`
	Runaway   Runaway  `json:"runaway"`
}

func Default() Config {
	return Config{
		Threshold: 30,
		Ignore:    []string{"tmux", "screen", "zellij", "ssh-agent", "gpg-agent", "mosh-server"},
		Stealth: Stealth{
			Interval:  "5m",
			Threshold: 70,
			Apps:      true,
			AppIdle:   "6h",
		},
		Runaway: Runaway{
			CPU:    90,
			For:    "10m",
			Memory: "4G",
			Ignore: []string{"ffmpeg", "cargo", "rustc", "xcodebuild", "swift-frontend", "clang", "make", "ninja"},
		},
	}
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Path is the config file location.
func Path() string { return filepath.Join(home(), ".config", "appsassin", "config.json") }

// SupportDir holds runtime state (pid file, app usage tracker).
func SupportDir() string {
	return filepath.Join(home(), "Library", "Application Support", "appsassin")
}

// LogPath is where stealth mode writes what it did.
func LogPath() string { return filepath.Join(home(), "Library", "Logs", "appsassin.log") }

// Load reads the config, writing defaults on first run so it's discoverable.
func Load() Config {
	c := Default()
	b, err := os.ReadFile(Path())
	if err != nil {
		_ = c.Save()
		return c
	}
	_ = json.Unmarshal(b, &c)
	return c
}

func (c Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(b, '\n'), 0o644)
}

// Ignored reports whether any of the identifiers match the ignore list.
// Entries match case-insensitively against the display name, executable
// basename, bundle ID or full path, and may use shell globs.
func (c Config) Ignored(ids ...string) bool { return matches(c.Ignore, ids) }

// Ignored reports whether the identifiers match the runaway ignore list,
// with the same rules as Config.Ignored.
func (r Runaway) Ignored(ids ...string) bool { return matches(r.Ignore, ids) }

func matches(list, ids []string) bool {
	for _, pat := range list {
		p := strings.ToLower(strings.TrimSpace(pat))
		if p == "" {
			continue
		}
		for _, id := range ids {
			if id == "" {
				continue
			}
			for _, v := range []string{strings.ToLower(id), strings.ToLower(filepath.Base(id))} {
				if v == p {
					return true
				}
				if ok, _ := filepath.Match(p, v); ok {
					return true
				}
			}
		}
	}
	return false
}

// AddIgnore appends an entry if it isn't already present.
func (c *Config) AddIgnore(id string) {
	for _, v := range c.Ignore {
		if strings.EqualFold(v, id) {
			return
		}
	}
	c.Ignore = append(c.Ignore, id)
}

func dur(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

func (s Stealth) IntervalDur() time.Duration { return dur(s.Interval, 5*time.Minute) }
func (s Stealth) AppIdleDur() time.Duration  { return dur(s.AppIdle, 6*time.Hour) }

func (r Runaway) ForDur() time.Duration { return dur(r.For, 10*time.Minute) }

// MemoryBytes parses Memory ("4G", "512M", "1.5GB", plain bytes); 0 means off.
func (r Runaway) MemoryBytes() int64 {
	s := strings.ToUpper(strings.TrimSpace(r.Memory))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "B"), "I")
	mult := 1.0
	if n := len(s); n > 0 {
		if i := strings.IndexByte("KMGT", s[n-1]); i >= 0 {
			mult = float64(int64(1) << (10 * (i + 1)))
			s = s[:n-1]
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int64(v * mult)
}

// Enabled reports whether either runaway check is on.
func (r Runaway) Enabled() bool { return r.CPU > 0 || r.MemoryBytes() > 0 }

// Describe sums the rule up, e.g. "≥90% CPU or ≥4G and growing, for 10m".
func (r Runaway) Describe() string {
	var parts []string
	if r.CPU > 0 {
		parts = append(parts, strconv.FormatFloat(r.CPU, 'f', -1, 64)+"% CPU")
	}
	if r.MemoryBytes() > 0 {
		parts = append(parts, strings.TrimSpace(r.Memory)+" and growing")
	}
	if len(parts) == 0 {
		return "off"
	}
	f := r.For
	if _, err := time.ParseDuration(f); err != nil {
		f = r.ForDur().String()
	}
	return "≥" + strings.Join(parts, " or ≥") + ", for " + f
}
