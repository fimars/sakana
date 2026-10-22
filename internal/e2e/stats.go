package e2e

import "runtime"

// processStats reports what the benchmark harness reads from the process
// rather than from the app: its CPU time and its goroutines. withMem adds
// the heap, which runtime.ReadMemStats reads by stopping the world, so the
// harness asks for it only between scenarios, never while it times one.
func processStats(withMem bool) map[string]any {
	out := map[string]any{"goroutines": runtime.NumGoroutine()}
	if user, sys := processCPUTime(); user > 0 || sys > 0 {
		out["cpu"] = map[string]any{
			"user":  user.Nanoseconds(),
			"sys":   sys.Nanoseconds(),
			"total": (user + sys).Nanoseconds(),
		}
	}
	if withMem {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		out["mem"] = map[string]any{
			"heapAlloc":  mem.HeapAlloc,
			"heapInuse":  mem.HeapInuse,
			"heapSys":    mem.HeapSys,
			"sys":        mem.Sys,
			"stackInuse": mem.StackInuse,
			"totalAlloc": mem.TotalAlloc,
			"mallocs":    mem.Mallocs,
			"frees":      mem.Frees,
			"numGC":      mem.NumGC,
		}
	}
	return out
}
