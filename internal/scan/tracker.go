package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mikemich/appsassin/internal/config"
)

// Tracker remembers when each app was last front-most, so "forgotten" means
// something. It's fed by the TUI while open and by the stealth daemon.
type Tracker struct {
	mu   sync.Mutex
	path string
	Last map[string]int64 `json:"last"` // bundle path -> unix seconds
}

func LoadTracker() *Tracker {
	t := &Tracker{path: filepath.Join(config.SupportDir(), "seen.json"), Last: map[string]int64{}}
	if b, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(b, t)
		if t.Last == nil {
			t.Last = map[string]int64{}
		}
	}
	return t
}

func (t *Tracker) Touch(key string, at time.Time) {
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if at.Unix() > t.Last[key] {
		t.Last[key] = at.Unix()
	}
}

func (t *Tracker) Get(key string) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.Last[key]
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(v, 0), true
}

// Save merges with whatever another appsassin process wrote, keeping the
// newest timestamp per app.
func (t *Tracker) Save() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	disk := struct {
		Last map[string]int64 `json:"last"`
	}{}
	if b, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(b, &disk)
	}
	for k, v := range disk.Last {
		if v > t.Last[k] {
			t.Last[k] = v
		}
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}
