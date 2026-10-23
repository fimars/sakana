package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// Store is what Sakana remembers between launches: the character image,
// whether the window floats, where on which screen it sits, and the rest
// reminder.
type Store struct {
	AlwaysOnTop bool    `json:"alwaysOnTop"`
	ScreenLabel string  `json:"screenLabel"` // empty: the primary display
	OffsetX     float64 `json:"offsetX"`     // DIPs from the right edge of the screen's work area
	OffsetY     float64 `json:"offsetY"`     // DIPs from the bottom edge
	ImagePath   string  `json:"imagePath"`   // empty: the bundled character
	// Cutout keys the image's background away and shows a white outline
	// around it.
	Cutout bool `json:"cutout"`

	// The rest reminder: a toast shows above the pet when a work phase
	// ends, and the next one starts when the rest phase ends.
	ReminderEnabled     bool    `json:"reminderEnabled"`
	ReminderText        string  `json:"reminderText"`
	ReminderWorkMinutes float64 `json:"reminderWorkMinutes"`
	ReminderRestMinutes float64 `json:"reminderRestMinutes"`
}

// DefaultOffset is where the window sits from the bottom-right corner
// until the user moves it.
const DefaultOffset = 64.0

// The rest reminder a fresh store starts with: 40 minutes of work, then
// the toast and 10 minutes of rest. The maxima are the settings window's
// limits, so a hand-edited store cannot ask for a timer that never runs.
const (
	DefaultReminderText        = "休息了！"
	DefaultReminderWorkMinutes = 40.0
	DefaultReminderRestMinutes = 10.0
	MaxReminderWorkMinutes     = 600.0
	MaxReminderRestMinutes     = 180.0
)

// defaultStore returns the settings a fresh launch starts with.
func defaultStore() *Store {
	return &Store{
		OffsetX:             DefaultOffset,
		OffsetY:             DefaultOffset,
		ReminderEnabled:     true,
		ReminderText:        DefaultReminderText,
		ReminderWorkMinutes: DefaultReminderWorkMinutes,
		ReminderRestMinutes: DefaultReminderRestMinutes,
	}
}

// Load reads the store from the user data directory, and returns the
// defaults for anything missing or unreadable.
func Load() *Store {
	s := defaultStore()
	data, err := os.ReadFile(path())
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, s); err != nil {
		return defaultStore()
	}
	if s.OffsetX == 0 && s.OffsetY == 0 {
		s.OffsetX, s.OffsetY = DefaultOffset, DefaultOffset
	}
	// Durations that cannot run, and an empty toast, fall back to the
	// defaults; a store written before the reminder existed has them all.
	s.ReminderWorkMinutes = ClampReminderMinutes(s.ReminderWorkMinutes, DefaultReminderWorkMinutes, MaxReminderWorkMinutes)
	s.ReminderRestMinutes = ClampReminderMinutes(s.ReminderRestMinutes, DefaultReminderRestMinutes, MaxReminderRestMinutes)
	if strings.TrimSpace(s.ReminderText) == "" {
		s.ReminderText = DefaultReminderText
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
		"cutout":      s.Cutout,

		"reminderEnabled":     s.ReminderEnabled,
		"reminderText":        s.ReminderText,
		"reminderWorkMinutes": s.ReminderWorkMinutes,
		"reminderRestMinutes": s.ReminderRestMinutes,
	}
}

// ClampReminderMinutes brings a reminder length into 1..max, or to def when
// it cannot run at all.
func ClampReminderMinutes(v, def, max float64) float64 {
	if v <= 0 {
		return def
	}
	return min(v, max)
}

func path() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "settings.json"
	}
	return filepath.Join(dir, "settings.json")
}
