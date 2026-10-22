package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/fimars/sakana/internal/e2e"
	"github.com/fimars/sakana/internal/pet"
	"github.com/fimars/sakana/internal/settings"
)

// app wires the pet, its settings and the menu bar together. Everything
// runs after the app is ready, so it may create windows and trays.
type app struct {
	store       *settings.Store
	widget      *pet.Widget
	petWin      *mygo.Window
	settingsWin *mygo.Window
	ui          *settings.UI
	tray        *mygo.Tray
	// quitting is set before the windows close, so the settings window lets
	// itself close instead of hiding, and the app can quit.
	quitting bool
}

func main() {
	mygo.App.SetName("Sakana")
	// An accessory app has no Dock icon and no app switcher entry, as the
	// Swift widget shipped with LSUIElement.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)

	a := &app{}
	mygo.App.WhenReady(a.setup)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *app) setup() {
	// The E2E harness keeps the user's real settings out of the way; a
	// packaged app never sets this.
	if dir := os.Getenv("SAKANA_E2E_USERDATA"); dir != "" {
		mygo.App.SetPath(mygo.PathUserData, dir)
	}
	a.store = settings.Load()
	bitmap, err := pet.LoadBitmap(a.store.ImagePath)
	if err != nil {
		log.Printf("sakana: cannot load the character image %q: %v; showing the bundled one", a.store.ImagePath, err)
		a.store.ImagePath = ""
		if bitmap, err = pet.DefaultBitmap(); err != nil {
			log.Fatalf("sakana: cannot decode the bundled character: %v", err)
		}
	}
	a.widget = pet.NewWidget(bitmap)

	w, h := a.petSize()
	x, y := a.positionFor(a.store.ScreenLabel, a.store.OffsetX, a.store.OffsetY, w, h)
	a.petWin = mygo.NewWindow(mygo.WindowOptions{
		Title:             "Sakana",
		Width:             w,
		Height:            h,
		X:                 x,
		Y:                 y,
		Frameless:         true,
		Transparent:       true,
		Panel:             true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		DisableShadow:     true,
		Content:           ui.View(a.widget.View),
	})
	// A panel floats above every app; the setting may lower it to the
	// normal level, as the Swift widget's "Always on Top" did.
	a.petWin.SetAlwaysOnTop(a.store.AlwaysOnTop)

	a.ui = &settings.UI{
		Store:            a.store,
		Bitmap:           a.widget.Bitmap,
		PickImage:        a.pickImage,
		RemoveImage:      a.removeImage,
		Displays:         a.displays,
		PetSize:          a.petSize,
		Apply:            a.applyPosition,
		ApplyAlwaysOnTop: a.applyAlwaysOnTop,
		Redraw:           a.redraw,
	}
	// UseContentSize: the Swift window had a 460×600 content rect, and the
	// ported view needs that much room for the coordinate pad and Apply.
	a.settingsWin = mygo.NewWindow(mygo.WindowOptions{
		Title:          "Sakana Settings",
		Width:          460,
		Height:         600,
		UseContentSize: true,
		MinWidth:       420,
		MinHeight:      588, // a frame min, so about 560 DIPs of content
		Hidden:         true,
		Content:        ui.View(a.ui.View),
	})
	// Closing the settings window hides it, unless the app is quitting:
	// preventing the close then would cancel the quit.
	a.settingsWin.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		a.settingsWin.Hide()
	})

	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "sakana",
		Icon:           trayIconPNG(),
		IconIsTemplate: true,
		ToolTip:        "Sakana",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.showSettings() }},
			{Label: "About Sakana", Click: func(*mygo.MenuItem, *mygo.Window) { a.showAbout() }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}),
	})
	if err != nil {
		log.Printf("sakana: cannot create the menu bar icon: %v", err)
	} else {
		a.tray = tray
	}

	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { a.persistPosition() })

	if sock := os.Getenv("SAKANA_E2E_SOCK"); sock != "" {
		if err := e2e.Start(sock, a); err != nil {
			log.Printf("sakana: e2e control server: %v", err)
		}
	}
}

// showSettings opens the settings window, which is also how the app takes
// the keyboard: a window of an accessory app becomes key when it shows.
func (a *app) showSettings() {
	a.ui.Reset()
	a.settingsWin.Show()
	a.settingsWin.Focus()
	mygo.App.Focus()
}

func (a *app) showAbout() {
	mygo.App.ShowAboutPanel(mygo.AboutPanelOptions{
		ApplicationName:    "Sakana",
		ApplicationVersion: mygo.App.Version(),
		Credits: "A wiggly little desktop companion.\n\n" +
			"Inspired by the lovely characters from Yuri works and the wonderful sakana-widget project.\n" +
			"github.com/dsrkafuu/sakana-widget",
	})
}

// petSize is the window size the current character needs.
func (a *app) petSize() (int, int) {
	m := a.widget.Metrics()
	return int(math.Round(m.Width)), int(math.Round(m.Height))
}

// positionFor places a w×h window offsetX, offsetY DIPs from the bottom-right
// corner of the work area of the display labeled label, or of the primary
// display when label is empty or that display is gone.
func (a *app) positionFor(label string, offsetX, offsetY float64, w, h int) (int, int) {
	wa := a.displayFor(label).WorkArea
	return wa.X + wa.Width - w - int(math.Round(offsetX)), wa.Y + wa.Height - h - int(math.Round(offsetY))
}

func (a *app) displayFor(label string) mygo.Display {
	displays := mygo.Screen.Displays()
	if label != "" {
		for _, d := range displays {
			if d.Label == label {
				return d
			}
		}
	}
	for _, d := range displays {
		if d.Primary {
			return d
		}
	}
	if len(displays) > 0 {
		return displays[0]
	}
	return mygo.Display{}
}

func (a *app) displays() []settings.Display {
	displays := mygo.Screen.Displays()
	out := make([]settings.Display, 0, len(displays))
	for _, d := range displays {
		out = append(out, settings.Display{Label: d.Label, WorkArea: d.WorkArea, Primary: d.Primary})
	}
	return out
}

// applyPosition saves where the window should sit and moves it there.
func (a *app) applyPosition(label string, offsetX, offsetY float64) {
	a.store.ScreenLabel, a.store.OffsetX, a.store.OffsetY = label, offsetX, offsetY
	w, h := a.petSize()
	x, y := a.positionFor(label, offsetX, offsetY, w, h)
	a.petWin.SetPosition(x, y)
	a.saveStore()
}

func (a *app) applyAlwaysOnTop(on bool) {
	a.store.AlwaysOnTop = on
	a.petWin.SetAlwaysOnTop(on)
	a.saveStore()
}

// pickImage shows the open panel off the main thread, then applies the
// chosen image on it. The dialog blocks until dismissed, so it may not run
// while MyGo draws a frame.
func (a *app) pickImage() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.settingsWin,
			Title:   "Choose Character Image",
			Message: "Choose a PNG or JPEG image to show as the character.",
			Filters: []mygo.FileFilter{{Name: "Images", Extensions: []string{"png", "jpg", "jpeg"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			log.Printf("sakana: cannot read %q: %v", paths[0], err)
			return
		}
		mygo.RunOnMain(func() {
			if err := a.setImageBytes(data, paths[0]); err != nil {
				log.Printf("sakana: cannot decode %q: %v", paths[0], err)
			}
		})
	}()
}

func (a *app) removeImage() {
	bitmap, err := pet.DefaultBitmap()
	if err != nil {
		log.Printf("sakana: cannot decode the bundled character: %v", err)
		return
	}
	a.applyBitmap(bitmap, "")
}

func (a *app) setImageBytes(data []byte, path string) error {
	bitmap, err := pet.LoadBitmapBytes(data)
	if err != nil {
		return err
	}
	a.applyBitmap(bitmap, path)
	return nil
}

// applyBitmap shows a new character, resizing the window to its viewport
// and keeping the base where it was, as the Swift controller did.
func (a *app) applyBitmap(bitmap *ui.Bitmap, path string) {
	old := a.petWin.Bounds()
	a.widget.SetBitmap(bitmap)
	w, h := a.petSize()
	a.petWin.SetBounds(mygo.Rectangle{
		X:      old.X + old.Width/2 - w/2,
		Y:      old.Y + old.Height - h,
		Width:  w,
		Height: h,
	})
	a.store.ImagePath = path
	a.saveStore()
	a.redraw()
}

func (a *app) redraw() {
	if a.petWin != nil {
		a.petWin.Update(func() {})
	}
	if a.settingsWin != nil {
		a.settingsWin.Update(func() {})
	}
}

func (a *app) saveStore() {
	if err := a.store.Save(); err != nil {
		log.Printf("sakana: cannot save settings: %v", err)
	}
}

// persistPosition records where the window ended up, in case it was moved
// since the settings were saved.
func (a *app) persistPosition() {
	if a.petWin == nil {
		return
	}
	b := a.petWin.Bounds()
	wa := a.displayFor(a.store.ScreenLabel).WorkArea
	a.store.OffsetX = float64(wa.X + wa.Width - b.X - b.Width)
	a.store.OffsetY = float64(wa.Y + wa.Height - b.Y - b.Height)
	a.saveStore()
}

// trayIconPNG draws the menu bar icon: a little fish, black on
// transparent, which macOS tints as a template image.
func trayIconPNG() []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	black := color.RGBA{A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := (float64(x) - 12.5) / 9
			dy := (float64(y) - 16) / 5.5
			body := dx*dx+dy*dy <= 1
			tail := x >= 20 && x <= 29 && math.Abs(float64(y)-16) <= float64(x-20)*0.55+0.6
			if body || tail {
				img.SetRGBA(x, y, black)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// The e2e control server drives the app through these; see internal/e2e.

func (a *app) State() map[string]any {
	var out map[string]any
	mygo.RunOnMain(func() { out = a.stateLocked() })
	return out
}

func (a *app) stateLocked() map[string]any {
	b := a.petWin.Bounds()
	m := a.widget.Metrics()
	st := a.widget.State()
	displays := make([]map[string]any, 0, 1)
	for _, d := range a.displays() {
		displays = append(displays, map[string]any{
			"label":   d.Label,
			"primary": d.Primary,
			"workArea": map[string]any{
				"x": d.WorkArea.X, "y": d.WorkArea.Y,
				"width": d.WorkArea.Width, "height": d.WorkArea.Height,
			},
		})
	}
	return map[string]any{
		"pid":         os.Getpid(),
		"bounds":      map[string]any{"x": b.X, "y": b.Y, "width": b.Width, "height": b.Height},
		"visible":     a.petWin.IsVisible(),
		"panel":       a.petWin.IsPanel(),
		"alwaysOnTop": a.petWin.IsAlwaysOnTop(),
		"settings":    a.store.Map(),
		"metrics": map[string]float64{
			"width": m.Width, "height": m.Height, "imageSize": m.ImageSize,
			"rodLength": m.RodLength, "baseWidth": m.BaseWidth, "baseHeight": m.BaseHeight,
			"basePad": m.BasePad, "rodVerticalScale": m.RodVerticalScale,
			"maxR": m.MaxR, "maxY": m.MaxY,
		},
		"physics":  map[string]float64{"r": st.R, "y": st.Y, "t": st.T, "w": st.W, "size": st.Size},
		"displays": displays,
	}
}

func (a *app) NativeWindow() uintptr { return a.petWin.NativeHandle() }

func (a *app) SetAlwaysOnTop(on bool) error {
	mygo.RunOnMain(func() { a.applyAlwaysOnTop(on) })
	return nil
}

func (a *app) ApplyOffsets(x, y float64) error {
	mygo.RunOnMain(func() { a.applyPosition(a.store.ScreenLabel, x, y) })
	return nil
}

func (a *app) Drag(dx, dy float64) error {
	mygo.RunOnMain(func() {
		a.widget.Drag(dx, dy)
		a.petWin.Invalidate()
	})
	return nil
}

func (a *app) SetImageBytes(data []byte) error {
	var err error
	mygo.RunOnMain(func() { err = a.setImageBytes(data, "") })
	return err
}

func (a *app) RequestFrame() { a.petWin.Update(func() {}) }

func (a *app) CapturePage() ([]byte, error) { return a.petWin.CapturePage() }

func (a *app) SaveAndReloadSettings() (map[string]any, map[string]any, error) {
	var before, after map[string]any
	var err error
	mygo.RunOnMain(func() {
		before = a.store.Map()
		if err = a.store.Save(); err != nil {
			return
		}
		after = settings.Load().Map()
	})
	return before, after, err
}

func (a *app) ShowSettings() error {
	mygo.RunOnMain(a.showSettings)
	return nil
}

func (a *app) CaptureSettings() ([]byte, error) {
	return a.settingsWin.CapturePage()
}

func (a *app) Quit() { mygo.App.Quit() }
