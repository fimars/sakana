//go:build darwin

package e2e

import (
	"errors"
	"strings"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo"
)

var sel = objc.RegisterName

// nativeInfo reads the NSWindow and NSApplication properties of ptr, the
// pet window's NSWindow*, as the harness checks them: the window class and
// style, its level and collection behavior, and the app's activation
// policy.
//
// AppKit must be messaged on the main thread, so the reads go through
// mygo.RunOnMain, which runs them there and waits.
func nativeInfo(ptr uintptr) (map[string]any, error) {
	if ptr == 0 {
		return nil, errors.New("e2e: the app reports no native window")
	}
	info := map[string]any{"nsWindowPtr": uint64(ptr)}
	mygo.RunOnMain(func() {
		w := objc.ID(ptr)
		app := objc.Send[objc.ID](objc.ID(objc.GetClass("NSApplication")), sel("sharedApplication"))
		info["class"] = objcString(objc.Send[objc.ID](w, sel("className")))
		info["styleMask"] = uint64(objc.Send[uint](w, sel("styleMask")))
		info["level"] = int(objc.Send[int](w, sel("level")))
		info["isOpaque"] = objc.Send[bool](w, sel("isOpaque"))
		info["hasShadow"] = objc.Send[bool](w, sel("hasShadow"))
		info["hidesOnDeactivate"] = objc.Send[bool](w, sel("hidesOnDeactivate"))
		info["ignoresMouseEvents"] = objc.Send[bool](w, sel("ignoresMouseEvents"))
		info["isKeyWindow"] = objc.Send[bool](w, sel("isKeyWindow"))
		info["isVisible"] = objc.Send[bool](w, sel("isVisible"))
		info["collectionBehavior"] = uint64(objc.Send[uint](w, sel("collectionBehavior")))
		info["activationPolicy"] = int(objc.Send[int](app, sel("activationPolicy")))
		info["appIsActive"] = objc.Send[bool](app, sel("isActive"))
	})
	return info, nil
}

// objcString reads an NSString, character by character, avoiding a raw
// pointer into the object's UTF-8 storage.
func objcString(id objc.ID) string {
	if id == 0 {
		return ""
	}
	n := objc.Send[uint](id, sel("length"))
	var b strings.Builder
	b.Grow(int(n))
	for i := uint(0); i < n; i++ {
		b.WriteRune(rune(objc.Send[uint16](id, sel("characterAtIndex:"), i)))
	}
	return b.String()
}
