package settings

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// Store is what Sakana remembers between launches: the character image,
// whether the window floats, and where on which screen it sits.
type Store struct {
	AlwaysOnTop bool    `json:"alwaysOnTop"`
	ScreenLabel string  `json:"screenLabel"` // empty: the primary display
	OffsetX     float64 `json:"offsetX"`     // DIPs from the right edge of the screen's work area
	OffsetY     float64 `json:"offsetY"`     // DIPs from the bottom edge
	ImagePath   string  `json:"imagePath"`   // empty: the bundled character
}

// DefaultOffset is where the window sits from the bottom-right corner
// until the user moves it.
const DefaultOffset = 64.0

// Load reads the store from the user data directory, and returns the
// defaults when there is none or it is unreadable.
func Load() *Store {
	s := &Store{OffsetX: DefaultOffset, OffsetY: DefaultOffset}
	data, err := os.ReadFile(path())
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, s); err != nil {
		return &Store{OffsetX: DefaultOffset, OffsetY: DefaultOffset}
	}
	if s.OffsetX == 0 && s.OffsetY == 0 {
		s.OffsetX, s.OffsetY = DefaultOffset, DefaultOffset
	}
	return s
}

// Save writes the store to the user data directory.
func (s *Store) Save() error {
	p := path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Map returns the store as a map, for diagnostics and the E2E harness.
func (s *Store) Map() map[string]any {
	return map[string]any{
		"alwaysOnTop": s.AlwaysOnTop,
		"screenLabel": s.ScreenLabel,
		"offsetX":     s.OffsetX,
		"offsetY":     s.OffsetY,
		"imagePath":   s.ImagePath,
	}
}

func path() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(dir, "settings.json")
}
