package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/fimars/sakana/internal/ca"
	"github.com/fimars/sakana/internal/e2e"
	"github.com/fimars/sakana/internal/pet"
	"github.com/fimars/sakana/internal/reminder"
	"github.com/fimars/sakana/internal/settings"
)

// The toast above the pet's head: the window it draws in, the gap between
// it and the top of the pet's window, and its look. The toast stays up
// until the pointer clicks it.
const (
	toastWidth  = 380
	toastHeight = 84
	toastGap    = 8
)

var (
	toastBackground      = ui.RGBA(30, 34, 42, 0.94)
	toastBackgroundHover = ui.RGBA(44, 49, 58, 0.96)
	toastForeground      = ui.RGB(255, 255, 255)
	toastCloseMark       = ui.RGBA(255, 255, 255, 0.55)
	toastCloseHover      = ui.RGBA(255, 255, 255, 0.14)
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
	reminder    *reminder.Scheduler
	// toastWin is the panel above the pet that shows the reminder, created
	// when the first toast shows; toastText is what its bubble says.
	toastWin  *mygo.Window
	toastText string
	// quitting is set before the windows close, so the settings window lets
	// itself close instead of hiding, and the app can quit.
	quitting bool
	// startedAt is when main ran, and phases when setup finished each step;
	// the benchmark harness reads them through Stats.
	startedAt time.Time
	phases    map[string]time.Time

	// The launch sway handed to Core Animation: the image the overlay
	// decodes, the simulated keyframes, and when the overlay started. All
	// main thread only, as the overlay is.
	imageBytes  []byte
	sway        *ca.Sway
	swayStates  []pet.State
	swayStarted time.Time
}

// mark records that setup finished the named step at now.
func (a *app) mark(step string) {
	if a.phases == nil {
		a.phases = make(map[string]time.Time, 8)
	}
	a.phases[step] = time.Now()
}

func main() {
	mygo.App.SetName("Sakana")
	// An accessory app has no Dock icon and no app switcher entry, as the
	// Swift widget shipped with LSUIElement.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)

	a := &app{startedAt: time.Now()}
	mygo.App.WhenReady(a.setup)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *app) setup() {
	a.mark("ready")
	// The E2E harness keeps the user's real settings out of the way; a
	// packaged app never sets this.
	if dir := os.Getenv("SAKANA_E2E_USERDATA"); dir != "" {
		mygo.App.SetPath(mygo.PathUserData, dir)
	}
	a.store = settings.Load()
	a.mark("store")
	bitmap, err := pet.LoadBitmap(a.store.ImagePath)
	if err != nil {
		log.Printf("sakana: cannot load the character image %q: %v; showing the bundled one", a.store.ImagePath, err)
		a.store.ImagePath = ""
		if bitmap, err = pet.DefaultBitmap(); err != nil {
			log.Fatalf("sakana: cannot decode the bundled character: %v", err)
		}
	}
	a.mark("image")
	a.widget = pet.NewWidget(bitmap)
	// The native sway decodes the image itself, so it keeps the bytes the
	// bitmap came from.
	if a.store.ImagePath != "" {
		if data, err := os.ReadFile(a.store.ImagePath); err == nil {
			a.imageBytes = data
		}
	}
	if len(a.imageBytes) == 0 {
		a.imageBytes = pet.DefaultBitmapBytes()
	}
	a.widget.NativeInterrupt = a.cancelNativeSway

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
	// The rest reminder runs on its own timer and draws nothing until a
	// work phase ends, so starting it costs the first frame nothing.
	a.reminder = reminder.New(a.reminderFired)
	a.reminder.Apply(a.reminderConfig())
	// The menu bar icon and the settings window are not on the way to the
	// first frame: setup after this point is what the user waits for to see
	// the pet, so the icon waits for the frame to reach the window.
	a.petWin.OnReadyToShow(func() {
		a.mark("firstPaint")
		a.createTray()
		a.startNativeSway()
	})
	a.mark("petWindow")

	a.ui = &settings.UI{
		Store:            a.store,
		Bitmap:           a.widget.Bitmap,
		PickImage:        a.pickImage,
		RemoveImage:      a.removeImage,
		Displays:         a.displays,
		PetSize:          a.petSize,
		Apply:            a.applyPosition,
		ApplyAlwaysOnTop: a.applyAlwaysOnTop,
		ApplyReminder:    a.applyReminder,
		Redraw:           a.redraw,
	}

	// Save where the pet sits while its window is still open: Mygo closes
	// the windows before OnWillQuit, and a closed window's Bounds is zero.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.quitting = true
		a.persistPosition()
		a.reminder.Stop()
		a.clearToast()
		a.cancelNativeSway()
	})

	if sock := os.Getenv("SAKANA_E2E_SOCK"); sock != "" {
		if err := e2e.Start(sock, a); err != nil {
			log.Printf("sakana: e2e control server: %v", err)
		}
	}
	a.mark("control")
}

// swayInterval is the pet's 100 Hz physics step. The overlay samples that
// trajectory every overlayStride steps: Core Animation interpolates between
// keyframes, so 33 Hz keyframes are smooth and cost a third of the numbers
// at 100 Hz.
const (
	swayInterval  = 10 * time.Millisecond
	overlayStride = 3
)

// startNativeSway simulates the launch sway and hands its keyframes to
// Core Animation, which animates them while the app draws no frames. It
// runs on the main thread, with the first Mygo frame already drawn.
func (a *app) startNativeSway() {
	if a.quitting || a.petWin == nil || len(a.imageBytes) == 0 {
		return
	}
	st := *a.widget.State()
	if st.Resting() {
		return
	}
	states := []pet.State{st}
	for !st.Resting() && len(states) < 5000 {
		st.Step(swayInterval)
		states = append(states, st)
	}
	if len(states) < 100 {
		// What the spring has left is under a second; the widget draws it
		// itself rather than hand an overlay over for it.
		return
	}
	frames := make([]ca.Frame, 0, len(states)/overlayStride+1)
	for i, s := range states {
		if i%overlayStride == 0 || i == len(states)-1 {
			frames = append(frames, ca.Frame{R: s.R, Y: s.Y})
		}
	}
	m := a.widget.Metrics()
	s, err := ca.Start(a.petWin.NativeHandle(), a.imageBytes, ca.Metrics{
		MidX:             m.MidX(),
		BaseTopY:         m.BaseTopY(),
		BaseWidth:        m.BaseWidth,
		BaseHeight:       m.BaseHeight,
		RodLength:        m.RodLength,
		RodVerticalScale: m.RodVerticalScale,
		ImageSize:        m.ImageSize,
	}, frames, swayInterval*overlayStride)
	if err != nil {
		log.Printf("sakana: native sway did not start: %v", err)
		return
	}
	a.sway, a.swayStates, a.swayStarted = s, states, time.Now()
	a.widget.SetNativeSway(true)
	go func() {
		<-s.Done()
		mygo.RunOnMain(func() { a.finishNativeSway(s) })
	}()
}

// finishNativeSway leaves the animation on its final frame, draws the same
// resting pose with Mygo, and removes the overlay once that frame is up.
func (a *app) finishNativeSway(s *ca.Sway) {
	if a.sway != s {
		return
	}
	*a.widget.State() = a.swayStates[len(a.swayStates)-1]
	a.sway, a.swayStates = nil, nil
	a.widget.SetNativeSway(false)
	a.petWin.Update(func() {})
	time.AfterFunc(120*time.Millisecond, func() { mygo.RunOnMain(s.Cancel) })
}

// cancelNativeSway hands the pose at this moment back to Mygo, for a drag,
// an image swap, or a quit before the animation ends.
func (a *app) cancelNativeSway() {
	if a.sway == nil {
		return
	}
	s := a.sway
	*a.widget.State() = a.poseAt(time.Since(a.swayStarted))
	a.sway, a.swayStates = nil, nil
	a.widget.SetNativeSway(false)
	s.Cancel()
	a.petWin.Update(func() {})
}

// poseAt is the keyframe elapsed into the sway, clamped to its ends.
func (a *app) poseAt(elapsed time.Duration) pet.State {
	i := int(elapsed / swayInterval)
	if i < 0 {
		i = 0
	}
	if i >= len(a.swayStates) {
		i = len(a.swayStates) - 1
	}
	return a.swayStates[i]
}

// nativeSwayLocked describes the overlay for the harness, and returns the
// pose to report in physics: while the overlay runs that is the keyframe at
// this moment, not the widget's stale state. It runs on the main thread.
func (a *app) nativeSwayLocked(st *pet.State) (map[string]any, pet.State) {
	pose := *st
	out := map[string]any{
		"active":     false,
		"r":          pose.R,
		"y":          pose.Y,
		"startedAt":  int64(0),
		"durationMs": float64(0),
		"frames":     0,
	}
	if a.sway == nil {
		return out, pose
	}
	pose = a.poseAt(time.Since(a.swayStarted))
	out["active"] = true
	out["r"] = pose.R
	out["y"] = pose.Y
	out["startedAt"] = a.swayStarted.UnixNano()
	out["durationMs"] = float64(time.Duration(len(a.swayStates)-1) * swayInterval / time.Millisecond)
	if r, x, y, ok := a.sway.Presentation(); ok {
		out["presentationR"] = r
		out["presentationX"] = x
		out["presentationY"] = y
	}
	return out, pose
}

// showSettings opens the settings window, which is also how the app takes
// the keyboard: a window of an accessory app becomes key when it shows.
func (a *app) showSettings() {
	win := a.settingsWindow()
	a.ui.Reset()
	win.Show()
	win.Focus()
	mygo.App.Focus()
}

// settingsWindow returns the settings window, creating it the first time it
// is needed: most launches never open it, and making it draws a frame of
// its view, which would otherwise cost startup time and memory.
func (a *app) settingsWindow() *mygo.Window {
	if a.settingsWin != nil {
		return a.settingsWin
	}
	// UseContentSize: the Swift window had a 460×600 content rect; the
	// ported view grew a rest reminder section and needs more room for the
	// coordinate pad and Apply.
	height := a.settingsHeight()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:          "Sakana Settings",
		Width:          460,
		Height:         height,
		UseContentSize: true,
		MinWidth:       420,
		MinHeight:      height - 20, // a frame min, so about 48 DIPs less content
		Hidden:         true,
		Content:        ui.View(a.ui.View),
	})
	// Closing the settings window hides it, unless the app is quitting:
	// preventing the close then would cancel the quit.
	win.OnClose(func(e *mygo.CloseEvent) {
		if a.quitting {
			return
		}
		e.PreventDefault()
		win.Hide()
	})
	a.settingsWin = win
	a.mark("settingsWindow")
	return win
}

// settingsHeight is the settings window's content height: what the view
// needs, or less on a display whose work area cannot hold it, where its
// scroll container shows the rest.
func (a *app) settingsHeight() int {
	const want = 800
	if wa := a.displayFor("").WorkArea; wa.Height > 0 {
		return max(min(want, wa.Height-80), 480)
	}
	return want
}

// createTray creates the menu bar icon once the first frame is drawn, so
// building it is not part of the way to a visible pet.
func (a *app) createTray() {
	if a.tray != nil {
		return
	}
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
		return
	}
	a.tray = tray
	a.mark("tray")
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
	if a.toastWin != nil {
		a.toastWin.SetAlwaysOnTop(on)
	}
	a.saveStore()
}

// minutesToDuration turns the settings' minutes into a duration.
func minutesToDuration(minutes float64) time.Duration {
	if minutes <= 0 {
		return 0
	}
	return time.Duration(min(minutes, settings.MaxReminderWorkMinutes) * float64(time.Minute))
}

// reminderConfig is the cycle the settings describe.
func (a *app) reminderConfig() reminder.Config {
	return reminder.Config{
		Enabled: a.store.ReminderEnabled,
		Work:    minutesToDuration(a.store.ReminderWorkMinutes),
		Rest:    minutesToDuration(a.store.ReminderRestMinutes),
		Text:    a.store.ReminderText,
	}
}

// applyReminder stores the reminder settings and applies them: a change of
// the cycle restarts it, while a change of the toast text alone leaves it
// running. The settings window calls it as its fields change, after the
// controls wrote the store, so the cycle's old shape comes from the
// scheduler.
func (a *app) applyReminder(enabled bool, text string, workMinutes, restMinutes float64) {
	workMinutes = settings.ClampReminderMinutes(workMinutes, settings.DefaultReminderWorkMinutes, settings.MaxReminderWorkMinutes)
	restMinutes = settings.ClampReminderMinutes(restMinutes, settings.DefaultReminderRestMinutes, settings.MaxReminderRestMinutes)
	old := a.reminder.Snapshot().Config
	a.store.ReminderEnabled = enabled
	a.store.ReminderText = text
	a.store.ReminderWorkMinutes = workMinutes
	a.store.ReminderRestMinutes = restMinutes
	a.saveStore()

	cfg := a.reminderConfig()
	if old.Enabled != cfg.Enabled || old.Work != cfg.Work || old.Rest != cfg.Rest {
		a.reminder.Apply(cfg)
		a.clearToast()
		return
	}
	a.reminder.SetText(cfg.Text)
}

// reminderFired shows the toast; the scheduler calls it from its own
// goroutine. The cycle may have been restarted or turned off while the
// toast waited for the main thread, in which case it is not shown.
func (a *app) reminderFired(text string) {
	mygo.RunOnMain(func() {
		if a.quitting {
			return
		}
		if snap := a.reminder.Snapshot(); !snap.Enabled || snap.Phase != reminder.Rest {
			return
		}
		a.showToast(text)
	})
}

// showToast shows text in the panel above the pet's head. The toast window
// is made the first time one shows, and a toast already up takes the new
// text.
func (a *app) showToast(text string) {
	if a.quitting || a.petWin == nil {
		return
	}
	// A blank message would show an empty pill; keep the default instead.
	// The store keeps what was typed, and Load fills it in when empty.
	if text = strings.TrimSpace(text); text == "" {
		text = settings.DefaultReminderText
	}
	a.ensureToastWindow()
	a.toastText = text

	b := a.petWin.Bounds()
	wa := a.displayFor(a.store.ScreenLabel).WorkArea
	x := b.X + b.Width/2 - toastWidth/2
	y := b.Y - toastHeight - toastGap
	if y < wa.Y {
		// No room above the pet, as when it sits at the top of the screen:
		// the toast goes below it instead.
		y = b.Y + b.Height + toastGap
	}
	x = clampInt(x, wa.X, wa.X+wa.Width-toastWidth)
	y = clampInt(y, wa.Y, wa.Y+wa.Height-toastHeight)
	a.toastWin.SetBounds(mygo.Rectangle{X: x, Y: y, Width: toastWidth, Height: toastHeight})
	a.toastWin.ShowInactive()
	a.toastWin.Invalidate()
}

// clearToast hides the toast, if one shows.
func (a *app) clearToast() {
	if a.toastWin != nil {
		a.toastWin.Hide()
	}
}

// ensureToastWindow creates the toast panel when the first toast shows.
func (a *app) ensureToastWindow() {
	if a.toastWin != nil {
		return
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:             "Sakana Reminder",
		Width:             toastWidth,
		Height:            toastHeight,
		Frameless:         true,
		Transparent:       true,
		Panel:             true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		DisableShadow:     true,
		Hidden:            true,
		Content:           ui.View(a.toastView),
	})
	// The toast follows the pet's window level, so turning always-on-top
	// off keeps reminders behind the apps in front, as the pet is.
	win.SetAlwaysOnTop(a.store.AlwaysOnTop)
	a.toastWin = win
}

// toastView draws the reminder: the text and a close button on a dark
// rounded bubble, centered in the transparent window. A click anywhere on
// the toast dismisses it, which the X makes plain to see.
func (a *app) toastView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	surface := ui.Box(c).Fill().Center()
	surface.Children(func() {
		bubble := ui.ButtonBase(c).
			Padding(8, 14).
			Radius(18).
			Background(toastBackground).
			AlignItems(ui.Center).
			Gap(10).
			Cursor(ui.CursorPointer).
			Label("Dismiss reminder")
		if bubble.Hovered() {
			bubble.Background(toastBackgroundHover)
		}
		bubble.Children(func() {
			ui.Text(c, a.toastText).
				FontSize(14).
				TextColor(toastForeground).
				MaxLines(2).
				MaxWidth(toastWidth - 120)
			closeButton(c, a)
		})
		if bubble.Clicked() {
			a.clearToast()
		}
	})
	// The bubble's margins are part of the window too: a click there
	// dismisses the toast rather than doing nothing.
	if surface.Clicked() {
		a.clearToast()
	}
}

// closeButton draws the X at the right of the toast bubble. The pointer over
// it shows a faint circle and the pointing hand; a click dismisses the
// toast. Painter.Line only paints axis-aligned segments, so the diagonals
// are a stroked path.
func closeButton(c *ui.Context, a *app) {
	button := ui.ButtonBase(c).
		Size(22, 22).
		Radius(11).
		Cursor(ui.CursorPointer).
		Label("Close")
	if button.Hovered() {
		button.Background(toastCloseHover)
	}
	button.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		var x ui.Path
		x.MoveTo(cx-3.5, cy-3.5)
		x.LineTo(cx+3.5, cy+3.5)
		x.MoveTo(cx+3.5, cy-3.5)
		x.LineTo(cx-3.5, cy+3.5)
		p.StrokePath(&x, 1.5, toastCloseMark)
	})
	if button.Clicked() {
		a.clearToast()
	}
}

// clampInt brings v into lo..hi.
func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
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
	a.imageBytes = pet.DefaultBitmapBytes()
	a.applyBitmap(bitmap, "")
}

func (a *app) setImageBytes(data []byte, path string) error {
	bitmap, err := pet.LoadBitmapBytes(data)
	if err != nil {
		return err
	}
	a.imageBytes = data
	a.applyBitmap(bitmap, path)
	return nil
}

// applyBitmap shows a new character, resizing the window to its viewport
// and keeping the base where it was, as the Swift controller did. Any
// running sway is cancelled first, and the new character greets with a
// native sway once the new bitmap is in and the window resized.
func (a *app) applyBitmap(bitmap *ui.Bitmap, path string) {
	a.cancelNativeSway()
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
	a.startNativeSway()
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
	snap := a.reminder.Snapshot()
	toast := map[string]any{"visible": false, "text": a.toastText}
	if a.toastWin != nil {
		tb := a.toastWin.Bounds()
		toast["visible"] = a.toastWin.IsVisible()
		toast["bounds"] = map[string]any{
			"x": tb.X, "y": tb.Y, "width": tb.Width, "height": tb.Height,
		}
	}
	nativeSway, pose := a.nativeSwayLocked(st)
	physics := map[string]float64{"r": pose.R, "y": pose.Y, "t": pose.T, "w": pose.W, "size": pose.Size}
	return map[string]any{
		"pid":         os.Getpid(),
		"bounds":      map[string]any{"x": b.X, "y": b.Y, "width": b.Width, "height": b.Height},
		"visible":     a.petWin.IsVisible(),
		"panel":       a.petWin.IsPanel(),
		"alwaysOnTop": a.petWin.IsAlwaysOnTop(),
		"settings":    a.store.Map(),
		"reminder": map[string]any{
			"enabled":     snap.Enabled,
			"text":        snap.Text,
			"workMinutes": snap.Work.Minutes(),
			"restMinutes": snap.Rest.Minutes(),
			"phase":       string(snap.Phase),
			"remainingMs": snap.Remaining.Milliseconds(),
			"shows":       snap.Shows,
			"toast":       toast,
		},
		"metrics": map[string]float64{
			"width": m.Width, "height": m.Height, "imageSize": m.ImageSize,
			"rodLength": m.RodLength, "baseWidth": m.BaseWidth, "baseHeight": m.BaseHeight,
			"basePad": m.BasePad, "rodVerticalScale": m.RodVerticalScale,
			"maxR": m.MaxR, "maxY": m.MaxY,
		},
		"physics":    physics,
		"displays":   displays,
		"nativeSway": nativeSway,
	}
}

// Stats reports the startup phases and the pet's frame telemetry, for the
// benchmark harness. It reads the phases on the main thread, as setup
// writes them there, and the frame counters from any goroutine.
func (a *app) Stats() map[string]any {
	var phases map[string]any
	var nativeSway map[string]any
	mygo.RunOnMain(func() {
		phases = make(map[string]any, len(a.phases)+1)
		phases["main"] = a.startedAt.UnixNano()
		for step, at := range a.phases {
			phases[step] = at.UnixNano()
		}
		nativeSway, _ = a.nativeSwayLocked(a.widget.State())
	})
	frames := a.widget.Stats()
	return map[string]any{
		"phases":     phases,
		"nativeSway": nativeSway,
		"frames": map[string]any{
			"total":      frames.Frames,
			"firstAt":    timeNanos(frames.FirstFrame),
			"lastAt":     timeNanos(frames.LastFrame),
			"idleAt":     timeNanos(frames.IdleAt),
			"buildTotal": frames.BuildTotal.Nanoseconds(),
			"buildMax":   frames.BuildMax.Nanoseconds(),
		},
	}
}

// timeNanos is t as unix nanoseconds, or 0 for the zero time.
func timeNanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
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
		a.cancelNativeSway()
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
	var data []byte
	var err error
	mygo.RunOnMain(func() {
		if a.settingsWin == nil {
			err = errors.New("sakana: the settings window is not open")
			return
		}
		data, err = a.settingsWin.CapturePage()
	})
	return data, err
}

// SetReminder applies the rest reminder settings, as the settings window's
// fields do.
func (a *app) SetReminder(enabled bool, text string, workMinutes, restMinutes float64) error {
	mygo.RunOnMain(func() { a.applyReminder(enabled, text, workMinutes, restMinutes) })
	return nil
}

// CaptureToast returns a PNG of what the toast window shows.
func (a *app) CaptureToast() ([]byte, error) {
	var data []byte
	var err error
	mygo.RunOnMain(func() {
		if a.toastWin == nil || !a.toastWin.IsVisible() {
			err = errors.New("sakana: the toast is not showing")
			return
		}
		data, err = a.toastWin.CapturePage()
	})
	return data, err
}

// ShowToast shows a toast now, for the E2E harness and for looking at a
// reminder without waiting for a phase to end.
func (a *app) ShowToast(text string) error {
	mygo.RunOnMain(func() { a.showToast(text) })
	return nil
}

// DismissToast hides the toast, as a click on it does.
func (a *app) DismissToast() error {
	mygo.RunOnMain(func() { a.clearToast() })
	return nil
}

func (a *app) Quit() { mygo.App.Quit() }
