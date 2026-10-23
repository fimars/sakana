#!/usr/bin/env python3
"""Measures how fast the packaged Sakana app starts, refreshes and rests.

It drives the running app over the e2e control socket, as tools/e2e.sh does:
internal/e2e serves GET /stats with the app's startup phases and frame
counters plus the process's own CPU time and heap. Every scenario is sized
to run in a few hundred milliseconds, so a number is big enough to notice a
change and small enough to iterate on, as the benchmark article argues.
Startup and the greeting sway are the exceptions: they are one-shot costs.

Output is artifacts/bench/report.md (integer milliseconds) and
artifacts/bench/bench.json (raw values). The app must be built first;
tools/bench.sh does that.
"""

import argparse
import ctypes
import json
import os
import shutil
import socket
import statistics
import subprocess
import sys
import tempfile
import time

# The benchmark targets the display's refresh rate: a forced refresh is one
# frame, and frames come no faster than the display shows them.


def display_hz():
    try:
        cg = ctypes.CDLL("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
        cg.CGMainDisplayID.restype = ctypes.c_uint32
        cg.CGDisplayCopyDisplayMode.restype = ctypes.c_void_p
        cg.CGDisplayCopyDisplayMode.argtypes = [ctypes.c_uint32]
        cg.CGDisplayModeGetRefreshRate.restype = ctypes.c_double
        cg.CGDisplayModeGetRefreshRate.argtypes = [ctypes.c_void_p]
        mode = cg.CGDisplayCopyDisplayMode(cg.CGMainDisplayID())
        hz = cg.CGDisplayModeGetRefreshRate(mode)
        return hz if hz >= 24 else 60.0
    except Exception:
        return 60.0


# libproc knows what ps reports: the process's true start time and resident
# size, which /stats cannot know about itself.
LIBPROC = ctypes.CDLL("/usr/lib/libproc.dylib")
LIBPROC.proc_pidinfo.restype = ctypes.c_int
LIBPROC.proc_pidinfo.argtypes = [
    ctypes.c_int,
    ctypes.c_int,
    ctypes.c_uint64,
    ctypes.c_void_p,
    ctypes.c_int,
]


def process_start(pid):
    buf = ctypes.create_string_buffer(256)
    if LIBPROC.proc_pidinfo(pid, 3, 0, buf, 256) <= 0:  # PROC_PIDTBSDINFO
        return None
    sec = ctypes.c_uint64.from_buffer(buf, 120).value
    usec = ctypes.c_uint64.from_buffer(buf, 128).value
    return sec + usec / 1e6


def resident_bytes(pid):
    buf = ctypes.create_string_buffer(256)
    if LIBPROC.proc_pidinfo(pid, 4, 0, buf, 256) <= 0:  # PROC_PIDTASKINFO
        return None
    return ctypes.c_uint64.from_buffer(buf, 8).value


def footprint(pid):
    """footprint reports what the app really holds, as vmmap summarizes it."""
    path = tempfile.mktemp(prefix="sakana-footprint.")
    try:
        proc = subprocess.run(
            ["/usr/bin/footprint", "-p", str(pid), "-j", path],
            capture_output=True,
            timeout=60,
        )
        if proc.returncode != 0:
            return None
        with open(path) as f:
            data = json.load(f)
        first = data["processes"][0]
        cats = {
            name: entry.get("dirty", 0) / 1e6
            for name, entry in first.get("categories", {}).items()
        }
        top = sorted(cats.items(), key=lambda kv: -kv[1])[:4]
        return {"total": first["footprint"] / 1e6, "top": top}
    except Exception:
        return None
    finally:
        try:
            os.unlink(path)
        except OSError:
            pass


class App:
    """A Sakana process and its control socket."""

    def __init__(self, exe):
        self.exe = exe
        self.tmp = tempfile.mkdtemp(prefix="sakana-bench.")
        self.sock = os.path.join(self.tmp, "pet.sock")
        self.proc = None
        self.log = os.path.join(self.tmp, "app.log")
        self.logfile = None

    def start(self, always_on_top=False):
        env = {k: v for k, v in os.environ.items() if not k.startswith("SAKANA_")}
        env.update(
            SAKANA_E2E_SOCK=self.sock,
            SAKANA_E2E_USERDATA=os.path.join(self.tmp, "userdata"),
            SAKANA_E2E_ARTIFACTS=os.path.join(self.tmp, "artifacts"),
        )
        os.makedirs(env["SAKANA_E2E_USERDATA"], exist_ok=True)
        if always_on_top:
            # Mygo holds a window's frames while nothing shows it, correctly:
            # an occluded pet draws nothing. The scenarios below animate the
            # pet, so it has to float over the other apps' windows: at the
            # default level a front window covers it and the sway would pause
            # mid-swing.
            with open(os.path.join(env["SAKANA_E2E_USERDATA"], "settings.json"), "w") as f:
                json.dump({"alwaysOnTop": True}, f)
        self.logfile = open(self.log, "w")
        self.proc = subprocess.Popen(
            [self.exe], env=env, stdout=self.logfile, stderr=subprocess.STDOUT
        )
        return self

    def http(self, method, path, body=b"", timeout=30):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(timeout)
        try:
            s.connect(self.sock)
            head = (
                f"{method} {path} HTTP/1.1\r\nHost: localhost\r\n"
                f"Content-Length: {len(body)}\r\nConnection: close\r\n\r\n"
            ).encode()
            s.sendall(head + body)
            data = b""
            while True:
                chunk = s.recv(65536)
                if not chunk:
                    break
                data += chunk
        finally:
            s.close()
        status_line, _, payload = data.partition(b"\r\n\r\n")
        return int(status_line.split(b" ")[1]), payload

    def get(self, path, timeout=30):
        return self.http("GET", path, timeout=timeout)

    def post(self, path, body=b"", timeout=30):
        return self.http("POST", path, body=body, timeout=timeout)

    def wait_health(self, timeout=15.0):
        """Waits for the control server and returns the wall time it answered."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.get("/health", timeout=1)[0] == 200:
                    return time.time()
            except OSError:
                pass
            if self.proc.poll() is not None:
                raise RuntimeError(
                    "the app exited before answering /health; log tail: "
                    + self.log_tail()
                )
            time.sleep(0.0002)
        raise RuntimeError("no /health within %.0fs; log tail: %s" % (timeout, self.log_tail()))

    def wait_phase(self, phase, timeout=10.0):
        """Waits for a startup phase mark to exist and returns its stats."""
        deadline = time.time() + timeout
        stats = self.stats()
        while time.time() < deadline:
            if phase in stats["phases"]:
                return stats
            time.sleep(0.002)
            stats = self.stats()
        raise RuntimeError("no %s phase within %.0fs" % (phase, timeout))

    def stats(self, mem=False):
        status, body = self.get("/stats?mem=1" if mem else "/stats")
        if status != 200:
            raise RuntimeError("GET /stats -> %d: %s" % (status, body[:200]))
        return json.loads(body)

    def quit(self, timeout=10.0):
        """Quits the app and returns how long the process took to go away."""
        started = time.time()
        try:
            self.post("/quit", timeout=1)
        except OSError:
            pass
        try:
            self.proc.wait(timeout=timeout)
            return (time.time() - started) * 1000
        except subprocess.TimeoutExpired:
            self.proc.kill()
            raise RuntimeError("the app did not quit within %.0fs" % timeout)

    def log_tail(self):
        try:
            with open(self.log, errors="replace") as f:
                return " | ".join(f.read().splitlines()[-6:])
        except OSError:
            return "(no log)"

    def close(self):
        if self.logfile is not None:
            self.logfile.close()
            self.logfile = None
        if self.proc is not None and self.proc.poll() is None:
            self.proc.kill()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                pass
        shutil.rmtree(self.tmp, ignore_errors=True)


def frame_count(app):
    return app.stats()["frames"]["total"]


def wait_for_frame(app, previous, timeout=2.0):
    """Waits until the app drew a frame after `previous` frames, and returns
    the milliseconds it took."""
    started = time.time()
    deadline = started + timeout
    while time.time() < deadline:
        if frame_count(app) > previous:
            return (time.time() - started) * 1000
        time.sleep(0.0005)
    raise RuntimeError("no frame within %.1fs" % timeout)


class Checks:
    def __init__(self):
        self.rows = []

    def add(self, name, ok, detail):
        self.rows.append((name, bool(ok), detail))

    def ok(self, name, detail):
        self.add(name, True, detail)

    def failed(self):
        return [row for row in self.rows if not row[1]]


# --------------------------------------------------------------- scenarios --


def cold_start(exe):
    """One launch: process start to the pet's first painted frame."""
    app = App(exe).start()
    try:
        health_at = app.wait_health()
        started = process_start(app.proc.pid)
        if started is None:
            raise RuntimeError("cannot read the process start time")
        stats = app.wait_phase("firstPaint")
        phases = stats["phases"]
        frames = stats["frames"]
        return {
            "spawn_to_main_ms": (phases["main"] - started * 1e9) / 1e6,
            "main_to_ready_ms": (phases["ready"] - phases["main"]) / 1e6,
            "ready_to_image_ms": (phases["image"] - phases["ready"]) / 1e6,
            "image_to_window_ms": (phases["petWindow"] - phases["image"]) / 1e6,
            "window_to_control_ms": (phases["control"] - phases["petWindow"]) / 1e6,
            "control_to_first_frame_ms": (frames["firstAt"] - phases["control"]) / 1e6,
            "first_frame_to_paint_ms": (phases["firstPaint"] - frames["firstAt"]) / 1e6,
            "spawn_to_first_paint_ms": (phases["firstPaint"] - started * 1e9) / 1e6,
            "spawn_to_health_ms": (health_at - started) * 1000,
            "frames": frames["total"],
        }
    finally:
        app.close()


def wait_for_rest(app, timeout=40.0):
    """Waits until the sway and the frames that follow it are done.

    The launch sway runs on Core Animation, so the app draws no Mygo frames
    while it moves. The native flag says whether that is expected. A window
    that nothing shows has its frames held, and the app never reaches its
    idle mark: the frame counter says whether the app stopped on purpose."""
    deadline = time.time() + timeout
    last_frames = None
    last_change = time.time()
    while time.time() < deadline:
        stats = app.stats()
        native = stats.get("nativeSway") or {}
        if native.get("active"):
            last_frames = None
            last_change = time.time()
        elif stats["frames"].get("idleAt"):
            return stats
        else:
            frames = stats["frames"]["total"]
            if frames != last_frames:
                last_frames, last_change = frames, time.time()
            elif time.time() - last_change > 3.0:
                raise RuntimeError(
                    "the pet's frames stopped without the sway resting (%d frames): "
                    "the display is asleep or the window is occluded; keep the "
                    "display awake while the benchmark runs" % frames)
        time.sleep(0.1)
    raise RuntimeError("the pet never came to rest within %.0fs" % timeout)


def sway_memory(exe):
    """What the app holds while the pet sways: Mygo keeps the frame it drew
    until the pet rests, and the swing takes seconds, so a launch holds it
    for that long. A separate launch because measuring it would pause the
    animation the timing scenario times."""
    app = App(exe).start(always_on_top=True)
    try:
        app.wait_health()
        app.wait_phase("firstPaint")
        time.sleep(2.0)
        hold = footprint(app.proc.pid)
        return {
            "sway_rss_mb": (resident_bytes(app.proc.pid) or 0) / 1e6,
            "sway_footprint_mb": hold["total"] if hold else None,
            "sway_footprint_top": hold["top"] if hold else [],
        }
    finally:
        app.close()


def wait_for_native(app, timeout=5.0):
    """Waits until the Core Animation sway is reported, or the app is idle.

    Polling /stats while the sway runs charges the harness's own CPU to the
    app. Read the native duration once and sleep through the animation."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        stats = app.stats()
        native = stats.get("nativeSway") or {}
        if native.get("active"):
            return stats, native
        if stats["frames"].get("idleAt"):
            return stats, {}
        time.sleep(0.02)
    raise RuntimeError("the native sway did not start")


def animation(app):
    """The greeting sway: what it costs and how long it lasts.

    The sway is a Core Animation overlay: the app draws one frame before it
    starts and one after it stops, so the Mygo frame counters stay still."""
    before = app.wait_phase("firstPaint")
    first_at = before["frames"]["firstAt"]
    cpu_before = before["cpu"]["total"]
    _, native = wait_for_native(app)
    if native:
        duration_ms = float(native.get("durationMs") or 0)
        time.sleep(max(0.2, duration_ms / 1000.0 + 0.4))
        rest = wait_for_rest(app, timeout=5.0)
    else:
        rest = wait_for_rest(app)
    idle_at = rest["frames"]["idleAt"]
    frames = max(0, rest["frames"]["total"] - before["frames"]["total"])
    seconds = duration_ms / 1000.0 if native else (idle_at - first_at) / 1e9
    cpu_ms = (rest["cpu"]["total"] - cpu_before) / 1e6
    return {
        "sway_ms": duration_ms if native else (idle_at - first_at) / 1e6,
        "sway_frames": frames,
        "sway_fps": 0.0,
        "sway_cpu_ms": cpu_ms,
        "cpu_per_second_ms": cpu_ms / seconds if seconds > 0 else 0,
        "frame_cpu_ms": 0.0,
        "frame_build_us": 0.0,
        "refreshes_per_100_cpu_ms": 0.0,
        "native": bool(native) and frames < 10 and seconds > 1.0,
    }


def idle(app):
    """One second at rest: no frames, no CPU, and what the app holds once
    Mygo has freed the frame it drew while the pet still moved."""
    time.sleep(2.2)  # Mygo frees the frame it drew 2 s after the last one
    before = app.stats()
    time.sleep(1.0)
    after = app.stats()
    pid = app.proc.pid
    hold = footprint(pid)
    heap = app.stats(mem=True)["mem"]
    return {
        "idle_frames": after["frames"]["total"] - before["frames"]["total"],
        "idle_cpu_ms": (after["cpu"]["total"] - before["cpu"]["total"]) / 1e6,
        "idle_rss_mb": (resident_bytes(pid) or 0) / 1e6,
        "idle_footprint_mb": hold["total"] if hold else None,
        "idle_footprint_top": hold["top"] if hold else [],
        "idle_heap_mb": heap["heapAlloc"] / 1e6,
        "idle_goroutines": after["goroutines"],
    }


def refresh(app, hz):
    """Forced refreshes: how long a request takes to become a drawn frame, and
    what a hundred of them cost the app. The gap between requests keeps them
    from coalescing into one frame."""
    for _ in range(2):  # the first refresh after an idle gap is AppKit-paced
        previous = frame_count(app)
        app.post("/frame")
        wait_for_frame(app, previous)
    latencies = []
    for _ in range(10):
        previous = frame_count(app)
        started = time.time()
        app.post("/frame")
        while time.time() - started < 2.0:
            if frame_count(app) > previous:
                break
            time.sleep(0.0005)
        else:
            raise RuntimeError("a forced refresh drew no frame")
        latencies.append((time.time() - started) * 1000)

    gap = max(0.0105, 1.25 / hz)
    count = max(8, round(0.3 / gap))
    before = app.stats()
    frames_before = before["frames"]["total"]
    cpu_before = before["cpu"]["total"]
    started = time.time()
    for _ in range(count):
        app.post("/frame")
        time.sleep(gap)
    wall_ms = (time.time() - started) * 1000
    after = app.stats()
    frames = after["frames"]["total"] - frames_before
    cpu_ms = (after["cpu"]["total"] - cpu_before) / 1e6
    return {
        "refresh_latency_ms": statistics.median(latencies),
        "refresh_count": count,
        "refresh_frames": frames,
        "refresh_wall_ms": wall_ms,
        "refresh_cpu_ms": cpu_ms,
        "refresh_cpu_per_100_ms": cpu_ms / max(frames, 1) * 100,
        "refresh_gap_ms": gap * 1000,
    }


def settings(app):
    """Opening the settings window, the first time and after, and writing the
    settings file: the refresh a settings change goes through."""
    cold = timed_post(app, "/settings/open")
    warm = [timed_post(app, "/settings/open") for _ in range(3)]
    # Size the reload batch so that it runs about as long as a refresh
    # batch: a few reloads are too fast to tell apart from fixed costs.
    probe = 20
    started = time.time()
    for _ in range(probe):
        app.post("/settings/reload")
    per_ms = (time.time() - started) * 1000 / probe
    count = min(4000, max(5, round(300 / max(per_ms, 0.01))))
    before = app.stats()
    cpu_before = before["cpu"]["total"]
    started = time.time()
    for _ in range(count):
        status, _ = app.post("/settings/reload")
        if status != 200:
            raise RuntimeError("POST /settings/reload -> %d" % status)
    wall_ms = (time.time() - started) * 1000
    cpu_ms = (app.stats()["cpu"]["total"] - cpu_before) / 1e6
    return {
        "settings_open_cold_ms": cold,
        "settings_open_warm_ms": statistics.median(warm),
        "settings_reload_count": count,
        "settings_reload_per_100_ms": wall_ms / count * 100,
        "settings_reload_cpu_per_100_ms": cpu_ms / count * 100,
    }


def timed_post(app, path):
    started = time.time()
    status, body = app.post(path)
    elapsed = (time.time() - started) * 1000
    if status != 200:
        raise RuntimeError("POST %s -> %d: %s" % (path, status, body[:200]))
    return elapsed


def image_refresh(app, png):
    """Choosing another character: decode and apply, then the native sway that
    shows it. Each swap kicks the spring, as changing the image does."""
    wait_for_rest(app)
    apply_ms = []
    frame_ms = []
    for _ in range(4):
        started = time.time()
        app.post("/image", body=png)
        apply_ms.append((time.time() - started) * 1000)
        deadline = time.time() + 2.0
        while time.time() < deadline:
            native = app.stats().get("nativeSway") or {}
            if native.get("active"):
                break
            time.sleep(0.005)
        else:
            raise RuntimeError("a swapped character did not start its native sway")
        frame_ms.append((time.time() - started) * 1000)
    return {
        "image_apply_ms": statistics.median(apply_ms),
        "image_frame_ms": statistics.median(frame_ms),
        "image_rss_mb": (resident_bytes(app.proc.pid) or 0) / 1e6,
    }


def bundle_size(path):
    try:
        out = subprocess.run(["du", "-sk", path], capture_output=True, text=True, timeout=60)
        return int(out.stdout.split()[0]) / 1024
    except Exception:
        return None


def human_ms(value):
    if value is None:
        return "-"
    if abs(value) >= 10:
        return "%.0f" % value
    return "%.1f" % value


# ----------------------------------------------------------------- report --


def report_facts(paths, hz):
    return {
        "generated": time.strftime("%Y-%m-%d %H:%M:%S %Z"),
        "host": "%s %s" % (os.uname().machine, subprocess.run(["sw_vers", "-productVersion"], capture_output=True, text=True).stdout.strip()),
        "go": subprocess.run(["go", "version"], capture_output=True, text=True).stdout.strip(),
        "mygo": subprocess.run(
            ["go", "list", "-m", "-f", "{{.Path}} {{.Version}}", "github.com/egoist/mygo"],
            capture_output=True,
            text=True,
        ).stdout.strip(),
        "commit": subprocess.run(["git", "rev-parse", "--short", "HEAD"], capture_output=True, text=True).stdout.strip(),
        "exe": os.path.relpath(paths["exe"]),
        "binary_mb": os.path.getsize(paths["exe"]) / 1e6,
        "bundle_mb": bundle_size(paths["bundle"]),
        "display_hz": hz,
    }


def write_report(out, facts, cold, session, checks):
    def table(header, rows):
        lines = ["| " + " | ".join(header) + " |"]
        lines.append("|" + "|".join("---" for _ in header) + "|")
        for row in rows:
            lines.append("| " + " | ".join(str(cell) for cell in row) + " |")
        return "\n".join(lines)

    cold_rows = []
    for key, label in [
        ("spawn_to_main_ms", "process start to main"),
        ("main_to_ready_ms", "main to AppKit ready"),
        ("ready_to_image_ms", "ready to character decoded"),
        ("image_to_window_ms", "character to pet window"),
        ("window_to_control_ms", "pet window to control server"),
        ("control_to_first_frame_ms", "control to first frame built"),
        ("first_frame_to_paint_ms", "first frame to first paint"),
    ]:
        values = [run[key] for run in cold]
        cold_rows.append(
            [label, human_ms(min(values)), human_ms(statistics.median(values)), human_ms(max(values))]
        )
    total = [run["spawn_to_first_paint_ms"] for run in cold]
    cold_rows.append(
        ["**process start to pet on screen**", human_ms(min(total)), human_ms(statistics.median(total)), human_ms(max(total))]
    )

    sway = session["animation"]
    refresh_rows = [
        ["animation", "Core Animation sway", "yes" if sway["native"] else "no"],
        ["animation", "CPU, ms per second", human_ms(sway["cpu_per_second_ms"])],
        ["animation", "CPU, ms per 100 Mygo frames", human_ms(sway["refreshes_per_100_cpu_ms"])],
        ["animation", "view build per Mygo frame, ms", "%.3f" % (sway["frame_build_us"] / 1000)],
        ["animation", "greeting sway, ms", human_ms(sway["sway_ms"])],
        ["animation", "greeting sway, Mygo frames", human_ms(sway["sway_frames"])],
        ["animation", "greeting sway, CPU ms", human_ms(sway["sway_cpu_ms"])],
        ["forced refresh", "request to drawn frame, ms", human_ms(session["refresh"]["refresh_latency_ms"])],
        ["forced refresh", "CPU, ms per 100 refreshes", human_ms(session["refresh"]["refresh_cpu_per_100_ms"])],
        ["forced refresh", "%d refreshes took, wall ms" % session["refresh"]["refresh_count"], human_ms(session["refresh"]["refresh_wall_ms"])],
    ]

    idle = session["idle"]
    sway_mem = session["sway"]
    resource_rows = [
        ["at rest", "frames drawn in 1 s", str(idle["idle_frames"])],
        ["at rest", "CPU, ms per second", human_ms(idle["idle_cpu_ms"])],
        ["at rest", "physical footprint, MB", human_ms(idle["idle_footprint_mb"])],
        ["at rest", "resident size, MB", human_ms(idle["idle_rss_mb"])],
        ["at rest", "Go heap, MB", human_ms(idle["idle_heap_mb"])],
        ["at rest", "goroutines", str(idle["idle_goroutines"])],
        ["while the pet sways", "physical footprint, MB", human_ms(sway_mem["sway_footprint_mb"])],
        ["while the pet sways", "resident size, MB", human_ms(sway_mem["sway_rss_mb"])],
        ["settings window open", "resident size, MB", human_ms(session["settings"]["settings_rss_mb"])],
        ["the app", "binary, MB", human_ms(facts["binary_mb"])],
        ["the app", "bundle, MB", human_ms(facts["bundle_mb"])],
    ]

    ops_rows = [
        ["settings window", "first open, ms", human_ms(session["settings"]["settings_open_cold_ms"])],
        ["settings window", "open again, ms", human_ms(session["settings"]["settings_open_warm_ms"])],
        ["settings file", "save and reload, ms per 100", human_ms(session["settings"]["settings_reload_per_100_ms"])],
        ["settings file", "save and reload, CPU ms per 100", human_ms(session["settings"]["settings_reload_cpu_per_100_ms"])],
        ["character image", "decode and apply, ms", human_ms(session["image"]["image_apply_ms"])],
        ["character image", "apply to native sway, ms", human_ms(session["image"]["image_frame_ms"])],
        ["quit", "request to process gone, ms", human_ms(session["quit_ms"])],
    ]

    passed = sum(1 for _, ok, _ in checks.rows if ok)
    failed = len(checks.failed())
    lines = []
    lines.append("# Sakana benchmark report")
    lines.append("")
    lines.append("- Generated: %s" % facts["generated"])
    lines.append("- Host: %s" % facts["host"])
    lines.append("- Go: %s" % facts["go"])
    lines.append("- Mygo: %s" % facts["mygo"])
    lines.append("- Commit: `%s`" % facts["commit"])
    lines.append("- App: `%s` (%.1f MB binary, %.1f MB bundle)" % (
        facts["exe"], facts["binary_mb"], facts["bundle_mb"] or 0))
    lines.append("- Display: %.0f Hz" % facts["display_hz"])
    lines.append("- Result: **%s** — %d invariants held, %d failed" % (
        "OK" if failed == 0 else "FAILED", passed, failed))
    lines.append("")
    lines.append("Times are milliseconds. `min` and `max` bound three cold launches; "
                 "a run on a busy machine is slower, so compare medians.")
    lines.append("")
    lines.append("The launch runs with a fresh userdata directory, as a first launch. "
                 "The scenario run has Always on Top on: Mygo holds a window's frames "
                 "while nothing shows it, so at the default level a front window covers "
                 "the pet and the sway pauses mid-swing.")
    lines.append("")
    lines.append("## Startup")
    lines.append("")
    lines.append("Each launch runs the packaged app with a fresh userdata directory, "
                 "so it is the first launch a user sees.")
    lines.append("")
    lines.append(table(["step", "min", "median", "max"], cold_rows))
    lines.append("")
    lines.append("The first two steps are the Go runtime and AppKit coming up; the rest "
                 "is the app's own setup, and only the pet on screen matters.")
    lines.append("")
    lines.append("## Refresh")
    lines.append("")
    lines.append("The greeting sway runs on a Core Animation overlay: the pet swings "
                 "once on launch, Mygo draws no frames while it moves, and the app "
                 "idles at 0 fps when it rests.")
    lines.append("")
    lines.append(table(["scenario", "measure", "ms"], refresh_rows))
    lines.append("")
    lines.append("## Settings and character")
    lines.append("")
    lines.append(table(["scenario", "measure", "ms"], ops_rows))
    lines.append("")
    lines.append("A character swap kicks the spring, as changing the image does in the "
                 "Swift widget, so it also costs one greeting sway.")
    lines.append("")
    lines.append("## Resources")
    lines.append("")
    lines.append(table(["scenario", "measure", "value"], resource_rows))
    if idle["idle_footprint_top"]:
        lines.append("")
        lines.append("Where the footprint goes: " + ", ".join(
            "%s %.1f MB" % (name, mb) for name, mb in idle["idle_footprint_top"]))
    lines.append("")
    lines.append("## Invariants")
    lines.append("")
    lines.append(table(
        ["check", "result", "detail"],
        [[name, "PASS" if ok else "FAIL", detail] for name, ok, detail in checks.rows],
    ))
    lines.append("")
    lines.append("## How to read this")
    lines.append("")
    lines.append("- A scenario runs for a few hundred milliseconds, so fixed costs such "
                 "as waking the app are small against what it measures.")
    lines.append("- Startup is the one-shot cost, measured to the frame that reaches the "
                 "screen: `spawn_to_first_paint_ms` is what a user waits for.")
    lines.append("- The greeting sway happens once per launch; `sway_cpu_ms` is the CPU "
                 "the app spends on it. Core Animation draws the intermediate frames, "
                 "so the app's Mygo frame count stays near zero. `ms per 100 Mygo "
                 "frames` applies to forced refreshes, not to the native sway.")
    lines.append("- At rest the app must draw no frames and use no CPU; that is checked, "
                 "not just reported. A refresh while the pet rests draws the one frame it "
                 "asks for: a resting spring is not stepped back into a swing.")
    lines.append("")
    with open(os.path.join(out, "report.md"), "w") as f:
        f.write("\n".join(lines) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--exe", required=True, help="the packaged app executable")
    parser.add_argument("--bundle", required=True, help="the .app bundle")
    parser.add_argument("--out", required=True, help="where the report goes")
    parser.add_argument("--cold-runs", type=int, default=3)
    args = parser.parse_args()
    os.makedirs(args.out, exist_ok=True)

    hz = display_hz()
    checks = Checks()
    png_path = os.path.join(os.path.dirname(__file__), "..", "internal", "pet", "assets", "takina.png")
    with open(png_path, "rb") as f:
        png = f.read()
    cold = []
    session = {
        "animation": {"sway_ms": 0, "sway_frames": 0, "sway_fps": 0, "sway_cpu_ms": 0,
                      "cpu_per_second_ms": 0, "frame_cpu_ms": 0, "frame_build_us": 0,
                      "refreshes_per_100_cpu_ms": 0, "native": False},
        "idle": {"idle_frames": 0, "idle_cpu_ms": 0, "idle_rss_mb": 0,
                 "idle_footprint_mb": None, "idle_footprint_top": [],
                 "idle_heap_mb": 0, "idle_goroutines": 0},
        "refresh": {"refresh_latency_ms": 0, "refresh_count": 0, "refresh_frames": 0,
                    "refresh_wall_ms": 0, "refresh_cpu_ms": 0,
                    "refresh_cpu_per_100_ms": 0, "refresh_gap_ms": 0},
        "settings": {"settings_open_cold_ms": 0, "settings_open_warm_ms": 0,
                     "settings_reload_count": 0, "settings_reload_per_100_ms": 0,
                     "settings_reload_cpu_per_100_ms": 0, "settings_rss_mb": 0},
        "image": {"image_apply_ms": 0, "image_frame_ms": 0, "image_rss_mb": 0},
        "sway": {"sway_rss_mb": 0, "sway_footprint_mb": None, "sway_footprint_top": []},
        "quit_ms": 0,
    }

    try:
        cold_start(args.exe)  # the file cache warms on the first launch
        print("== startup: %d cold launches" % args.cold_runs)
        for i in range(args.cold_runs):
            run = cold_start(args.exe)
            cold.append(run)
            print("  launch %d: %.0f ms to pet on screen (%.0f ms to main)"
                  % (i + 1, run["spawn_to_first_paint_ms"], run["spawn_to_main_ms"]))
            checks.add(
                "startup.paint.%d" % (i + 1),
                run["spawn_to_first_paint_ms"] < 5000 and run["frames"] >= 1,
                "pet painted after %.0f ms, %d frame(s)"
                % (run["spawn_to_first_paint_ms"], run["frames"]),
            )

        print("== memory while the pet sways")
        session["sway"] = sway_memory(args.exe)
        print("  %.0f MB footprint, %.0f MB resident while swaying" % (
            session["sway"]["sway_footprint_mb"] or 0, session["sway"]["sway_rss_mb"]))

        print("== launch the app for the scenarios (Always on Top, so the pet stays visible)")
        app = App(args.exe).start(always_on_top=True)
        try:
            app.wait_health()
            session["animation"] = animation(app)
            sway = session["animation"]
            print("  greeting sway: %.0f ms, %d Mygo frames, %.0f ms CPU%s"
                  % (sway["sway_ms"], sway["sway_frames"], sway["sway_cpu_ms"],
                     " (Core Animation)" if sway["native"] else ""))
            checks.add("animation.rest", sway["sway_ms"] > 1000,
                       "the sway lasted %.0f ms with %d Mygo frames"
                       % (sway["sway_ms"], sway["sway_frames"]))

            session["idle"] = idle(app)
            row = session["idle"]
            print("  at rest: %d frames, %.1f ms CPU in 1 s, %.0f MB resident"
                  % (row["idle_frames"], row["idle_cpu_ms"], row["idle_rss_mb"]))
            checks.add("idle.no_frames", row["idle_frames"] == 0,
                       "%d frames drawn in 1 s" % row["idle_frames"])
            checks.add("idle.no_cpu", row["idle_cpu_ms"] < 10,
                       "%.1f ms CPU in 1 s" % row["idle_cpu_ms"])

            session["refresh"] = refresh(app, hz)
            row = session["refresh"]
            print("  refresh: %.1f ms request to frame, %.0f ms CPU per 100"
                  % (row["refresh_latency_ms"], row["refresh_cpu_per_100_ms"]))
            checks.add("refresh.frames",
                       row["refresh_frames"] >= row["refresh_count"] - 2,
                       "%d of %d refreshes drew a frame"
                       % (row["refresh_frames"], row["refresh_count"]))

            session["settings"] = settings(app)
            session["settings"]["settings_rss_mb"] = (resident_bytes(app.proc.pid) or 0) / 1e6
            row = session["settings"]
            print("  settings: first open %.1f ms, open again %.1f ms"
                  % (row["settings_open_cold_ms"], row["settings_open_warm_ms"]))
            checks.add("settings.open", row["settings_open_cold_ms"] < 2000,
                       "first open after %.1f ms" % row["settings_open_cold_ms"])

            session["image"] = image_refresh(app, png)
            row = session["image"]
            print("  image: decode and apply %.1f ms, native sway %.1f ms later"
                  % (row["image_apply_ms"], row["image_frame_ms"]))
            checks.add("image.refresh", row["image_frame_ms"] < 1000,
                       "a swapped character started its native sway after %.1f ms"
                       % row["image_frame_ms"])

            session["quit_ms"] = app.quit()
            print("  quit: %.0f ms" % session["quit_ms"])
            checks.ok("quit", "the app quit in %.0f ms" % session["quit_ms"])
        finally:
            app.close()
    except Exception as err:  # a broken run still leaves a report to read
        checks.add("harness", False, "%s: %s" % (type(err).__name__, err))
        print("bench: %s" % err, file=sys.stderr)

    facts = report_facts({"exe": args.exe, "bundle": args.bundle}, hz)
    write_report(args.out, facts, cold, session, checks)
    raw = {
        "facts": facts,
        "startup": cold,
        "session": session,
        "checks": [{"check": name, "ok": ok, "detail": detail}
                   for name, ok, detail in checks.rows],
    }
    with open(os.path.join(args.out, "bench.json"), "w") as f:
        json.dump(raw, f, indent=2)
        f.write("\n")

    failed = checks.failed()
    if failed:
        print()
        for name, _, detail in failed:
            print("FAIL %-24s %s" % (name, detail), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
