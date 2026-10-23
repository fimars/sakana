// Package e2e is the app's side of the end-to-end harness: a control
// server the app serves on a Unix socket when SAKANA_E2E_SOCK is set, which
// tools/e2e.sh drives to check the running app.
package e2e

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Hook is what the control server calls in the app. The app implements it
// in main.go; nothing here imports the app.
type Hook interface {
	// State is the app's state to check: the pet window's bounds, the
	// spring's, the viewport's and the settings'.
	State() map[string]any
	// Stats is the app's startup phases and frame telemetry, which the
	// benchmark harness measures with the process's own statistics.
	Stats() map[string]any
	// NativeWindow returns the pet window's NSWindow*.
	NativeWindow() uintptr
	// SetAlwaysOnTop applies the floating toggle.
	SetAlwaysOnTop(on bool) error
	// ApplyOffsets saves and applies the position offsets from the
	// bottom-right corner.
	ApplyOffsets(x, y float64) error
	// Drag pulls the character by dx, dy DIPs from where the press began.
	Drag(dx, dy float64) error
	// SetImageBytes applies a PNG or JPEG as the character image.
	SetImageBytes(data []byte) error
	// SetCutout applies the cutout toggle, as the settings checkbox does.
	SetCutout(on bool) error
	// CaptureCharacter returns a PNG of the character image's active
	// pixels.
	CaptureCharacter() ([]byte, error)
	// RequestFrame asks the pet window to draw another frame.
	RequestFrame()
	// CapturePage returns a PNG of what the pet window shows.
	CapturePage() ([]byte, error)
	// SaveAndReloadSettings writes the settings and reads them back.
	SaveAndReloadSettings() (before, after map[string]any, err error)
	// SetReminder applies the rest reminder settings, as the settings
	// window's fields do.
	SetReminder(enabled bool, text string, workMinutes, restMinutes float64) error
	// CaptureToast returns a PNG of what the reminder toast shows.
	CaptureToast() ([]byte, error)
	// ShowToast shows a reminder toast at once, for its look.
	ShowToast(text string) error
	// DismissToast hides the toast, as a click on it does.
	DismissToast() error
	// ShowSettings opens the settings window as the menu bar item does.
	ShowSettings() error
	// CaptureSettings returns a PNG of what the settings window shows.
	CaptureSettings() ([]byte, error)
	// Quit quits the app.
	Quit()
}

// maxImageBody bounds a POST /image body: a character image is small.
const maxImageBody = 32 << 20

// artifactsDir is where captures are written: SAKANA_E2E_ARTIFACTS when
// set, so the harness keeps them together, and artifacts/e2e otherwise.
func artifactsDir() string {
	if dir := os.Getenv("SAKANA_E2E_ARTIFACTS"); dir != "" {
		return dir
	}
	return filepath.Join("artifacts", "e2e")
}

// colorShare is one 5-bit color bucket among a capture's opaque pixels:
// its center and its share of those pixels.
type colorShare struct {
	R     uint8   `json:"r"`
	G     uint8   `json:"g"`
	B     uint8   `json:"b"`
	Share float64 `json:"share"`
}

// captureStats is what GET /capture reports about a capture: where it was
// written and what its pixels say about the window behind them.
type captureStats struct {
	Path           string       `json:"path"`
	Bytes          int          `json:"bytes"`
	Width          int          `json:"width"`
	Height         int          `json:"height"`
	AlphaZeroRatio float64      `json:"alphaZeroRatio"`
	CornerAlphas   cornerAlphas `json:"cornerAlphas"`
	CenterAlpha    uint8        `json:"centerAlpha"`
	OpaqueBBox     *box         `json:"opaqueBBox"`
	Colors         []colorShare `json:"colors"`
}

type cornerAlphas struct {
	TopLeft     uint8 `json:"topLeft"`
	TopRight    uint8 `json:"topRight"`
	BottomLeft  uint8 `json:"bottomLeft"`
	BottomRight uint8 `json:"bottomRight"`
}

// box is the bounding box of the pixels that are not fully transparent.
type box struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Start begins serving the control API on the Unix socket at sock. It is
// called only when SAKANA_E2E_SOCK is set, so a packaged app never serves
// it.
func Start(sock string, h Hook) error {
	if sock == "" {
		return errors.New("e2e: empty control socket path")
	}
	if h == nil {
		return errors.New("e2e: nil hook")
	}
	// A socket left behind by a crashed run would make Listen fail.
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("e2e: remove stale socket %s: %w", sock, err)
	}
	listener, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("e2e: listen on %s: %w", sock, err)
	}
	_ = os.Chmod(sock, 0o600)

	s := &server{h: h, artifacts: artifactsDir(), sock: sock}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /state", s.state)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /native", s.native)
	mux.HandleFunc("GET /capture", s.capturePet)
	mux.HandleFunc("GET /settings/capture", s.captureSettings)
	mux.HandleFunc("GET /toast/capture", s.captureToast)
	mux.HandleFunc("POST /toast/show", s.toastShow)
	mux.HandleFunc("POST /toast/dismiss", s.toastDismiss)
	mux.HandleFunc("POST /aot", s.aot)
	mux.HandleFunc("POST /offsets", s.offsets)
	mux.HandleFunc("POST /drag", s.drag)
	mux.HandleFunc("POST /image", s.image)
	mux.HandleFunc("POST /cutout", s.cutout)
	mux.HandleFunc("GET /character/capture", s.captureCharacter)
	mux.HandleFunc("POST /frame", s.frame)
	mux.HandleFunc("POST /reminder", s.reminder)
	mux.HandleFunc("POST /settings/open", s.settingsOpen)
	mux.HandleFunc("POST /settings/reload", s.settingsReload)
	mux.HandleFunc("POST /quit", s.quit)

	go func() {
		if err := http.Serve(listener, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("e2e: control server on %s: %v", sock, err)
		}
	}()
	log.Printf("e2e: control server on %s, captures in %s", sock, s.artifacts)
	return nil
}

type server struct {
	h         Hook
	artifacts string
	sock      string
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.h.State())
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	out := s.h.Stats()
	withMem, _ := boolParam(r, "mem")
	for key, value := range processStats(withMem) {
		out[key] = value
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) native(w http.ResponseWriter, _ *http.Request) {
	info, err := nativeInfo(s.h.NativeWindow())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *server) capturePet(w http.ResponseWriter, r *http.Request) {
	name, err := captureName(r, "pet")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	stats, err := s.capture(name, s.h.CapturePage)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *server) captureSettings(w http.ResponseWriter, r *http.Request) {
	name, err := captureName(r, "settings")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	stats, err := s.capture(name, s.h.CaptureSettings)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *server) captureToast(w http.ResponseWriter, r *http.Request) {
	name, err := captureName(r, "toast")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	stats, err := s.capture(name, s.h.CaptureToast)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// toastRequest is what POST /toast/show takes.
type toastRequest struct {
	Text string `json:"text"`
}

func (s *server) toastShow(w http.ResponseWriter, r *http.Request) {
	var req toastRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("e2e: decode toast: %w", err))
		return
	}
	if err := s.h.ShowToast(req.Text); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.h.State()})
}

func (s *server) toastDismiss(w http.ResponseWriter, _ *http.Request) {
	if err := s.h.DismissToast(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.h.State()})
}

// capture saves what fn captured under the artifacts directory, as
// name.png, and returns the statistics of its alpha channel.
func (s *server) capture(name string, fn func() ([]byte, error)) (captureStats, error) {
	var stats captureStats
	data, err := fn()
	if err != nil {
		return stats, fmt.Errorf("e2e: capture %s: %w", name, err)
	}
	if len(data) == 0 {
		return stats, fmt.Errorf("e2e: capture %s returned no image", name)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return stats, fmt.Errorf("e2e: decode %s capture: %w", name, err)
	}
	if err := os.MkdirAll(s.artifacts, 0o755); err != nil {
		return stats, fmt.Errorf("e2e: create artifacts directory: %w", err)
	}
	path := filepath.Join(s.artifacts, name+".png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return stats, fmt.Errorf("e2e: write %s capture: %w", name, err)
	}
	return statsFor(path, len(data), img), nil
}

// statsFor summarizes a capture: how much of it is transparent, the alpha
// of its corners and center, the box its visible pixels occupy, and the
// largest color buckets among its opaque pixels.
func statsFor(path string, size int, img image.Image) captureStats {
	b := img.Bounds()
	stats := captureStats{
		Path:  path,
		Bytes: size,
		Width: b.Dx(), Height: b.Dy(),
		CornerAlphas: cornerAlphas{
			TopLeft:     alphaAt(img, b.Min.X, b.Min.Y),
			TopRight:    alphaAt(img, b.Max.X-1, b.Min.Y),
			BottomLeft:  alphaAt(img, b.Min.X, b.Max.Y-1),
			BottomRight: alphaAt(img, b.Max.X-1, b.Max.Y-1),
		},
		CenterAlpha: alphaAt(img, b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2),
	}
	transparent := 0
	buckets := make(map[[3]uint8]int)
	opaque := 0
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			a := alphaAt(img, x, y)
			if a == 0 {
				transparent++
				continue
			}
			if a >= 128 {
				r, g, bl, _ := img.At(x, y).RGBA()
				buckets[[3]uint8{uint8(r >> 11), uint8(g >> 11), uint8(bl >> 11)}]++
				opaque++
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if total := b.Dx() * b.Dy(); total > 0 {
		stats.AlphaZeroRatio = float64(transparent) / float64(total)
	}
	if maxX >= minX && maxY >= minY {
		stats.OpaqueBBox = &box{
			X: minX - b.Min.X, Y: minY - b.Min.Y,
			Width: maxX - minX + 1, Height: maxY - minY + 1,
		}
	}
	stats.Colors = topColors(buckets, opaque)
	return stats
}

// topColors is the up to six largest 5-bit color buckets of the opaque
// pixels, as their centers and their share of those pixels.
func topColors(buckets map[[3]uint8]int, opaque int) []colorShare {
	if opaque == 0 {
		return nil
	}
	type counted struct {
		color colorShare
		n     int
	}
	all := make([]counted, 0, len(buckets))
	for k, n := range buckets {
		all = append(all, counted{
			color: colorShare{
				R:     k[0]<<3 | 4,
				G:     k[1]<<3 | 4,
				B:     k[2]<<3 | 4,
				Share: float64(n) / float64(opaque),
			},
			n: n,
		})
	}
	slices.SortFunc(all, func(i, j counted) int { return cmp.Compare(j.n, i.n) })
	if len(all) > 6 {
		all = all[:6]
	}
	out := make([]colorShare, len(all))
	for i, c := range all {
		out[i] = c.color
	}
	return out
}

// alphaAt is the pixel's alpha as 8 bits, whatever the image's color model.
func alphaAt(img image.Image, x, y int) uint8 {
	_, _, _, a := img.At(x, y).RGBA()
	return uint8(a >> 8)
}

func (s *server) aot(w http.ResponseWriter, r *http.Request) {
	on, err := boolParam(r, "on")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	if err := s.h.SetAlwaysOnTop(on); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "on": on})
}

func (s *server) offsets(w http.ResponseWriter, r *http.Request) {
	x, err := floatParam(r, "x")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	y, err := floatParam(r, "y")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	if err := s.h.ApplyOffsets(x, y); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "x": x, "y": y})
}

func (s *server) drag(w http.ResponseWriter, r *http.Request) {
	dx, err := floatParam(r, "dx")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	dy, err := floatParam(r, "dy")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	if err := s.h.Drag(dx, dy); err != nil {
		writeError(w, err)
		return
	}
	s.h.RequestFrame()
	// The spring steps while frames go by, so report the state right after
	// the drag rather than let a later GET /state see it decayed.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "dx": dx, "dy": dy, "state": s.h.State(),
	})
}

func (s *server) image(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageBody))
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("e2e: read image: %w", err))
		return
	}
	if len(body) == 0 {
		writeErrorStatus(w, http.StatusBadRequest, errors.New("e2e: empty image body"))
		return
	}
	if err := s.h.SetImageBytes(body); err != nil {
		writeError(w, err)
		return
	}
	s.h.RequestFrame()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "bytes": len(body)})
}

func (s *server) frame(w http.ResponseWriter, _ *http.Request) {
	s.h.RequestFrame()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) cutout(w http.ResponseWriter, r *http.Request) {
	on, err := boolParam(r, "on")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	if err := s.h.SetCutout(on); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "on": on, "state": s.h.State()})
}

func (s *server) captureCharacter(w http.ResponseWriter, r *http.Request) {
	name, err := captureName(r, "character")
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	stats, err := s.capture(name, s.h.CaptureCharacter)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// reminderRequest is what POST /reminder takes: the rest reminder settings,
// in the minutes the settings window shows.
type reminderRequest struct {
	Enabled     bool    `json:"enabled"`
	Text        string  `json:"text"`
	WorkMinutes float64 `json:"workMinutes"`
	RestMinutes float64 `json:"restMinutes"`
}

func (s *server) reminder(w http.ResponseWriter, r *http.Request) {
	var req reminderRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("e2e: decode reminder settings: %w", err))
		return
	}
	if err := s.h.SetReminder(req.Enabled, req.Text, req.WorkMinutes, req.RestMinutes); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": s.h.State()})
}

func (s *server) settingsOpen(w http.ResponseWriter, _ *http.Request) {
	if err := s.h.ShowSettings(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) settingsReload(w http.ResponseWriter, _ *http.Request) {
	before, after, err := s.h.SaveAndReloadSettings()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "before": before, "after": after,
		"same": reflect.DeepEqual(before, after),
	})
}

func (s *server) quit(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quitting": true})
	// Answer first: the app goes away, and its socket with it.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.Remove(s.sock)
		s.h.Quit()
	}()
}

// captureName validates the name a capture is saved under, defaulting to
// def. It keeps a name from escaping the artifacts directory.
var captureNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func captureName(r *http.Request, def string) (string, error) {
	name := r.URL.Query().Get("name")
	if name == "" {
		return def, nil
	}
	if !captureNameRE.MatchString(name) || name == "." || name == ".." || strings.Contains(name, "..") {
		return "", fmt.Errorf("e2e: invalid capture name %q", name)
	}
	return name, nil
}

func boolParam(r *http.Request, key string) (bool, error) {
	raw := r.URL.Query().Get(key)
	switch strings.ToLower(raw) {
	case "1", "true", "on", "yes":
		return true, nil
	case "0", "false", "off", "no":
		return false, nil
	}
	return false, fmt.Errorf("e2e: query parameter %q must be 1 or 0, got %q", key, raw)
}

func floatParam(r *http.Request, key string) (float64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, fmt.Errorf("e2e: missing query parameter %q", key)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("e2e: query parameter %q is not a number: %q", key, raw)
	}
	return v, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	writeErrorStatus(w, http.StatusInternalServerError, err)
}

func writeErrorStatus(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}
