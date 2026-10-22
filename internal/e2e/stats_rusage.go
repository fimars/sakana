//go:build darwin || linux || freebsd || netbsd || openbsd

package e2e

import (
	"syscall"
	"time"
)

// processCPUTime is the process's user and system CPU time since it
// started: the benchmark harness measures a scenario by the difference
// between two reads.
func processCPUTime() (user, sys time.Duration) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	return timeval(ru.Utime), timeval(ru.Stime)
}

func timeval(tv syscall.Timeval) time.Duration {
	return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
}
