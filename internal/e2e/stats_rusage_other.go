//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd

package e2e

import "time"

// processCPUTime reports no CPU time where the process's rusage is not
// available, as on Windows: the harness then only has the frame counters.
func processCPUTime() (user, sys time.Duration) { return 0, 0 }
