//go:build !darwin

package e2e

// nativeInfo has nothing to probe away from macOS: the window is not an
// NSWindow. Report an empty map rather than make other platforms fail.
func nativeInfo(uintptr) (map[string]any, error) {
	return map[string]any{}, nil
}
