#!/usr/bin/env python3
"""Mouse-protocol verification for squint (spec §11 checklist).

Drives a real pty, injects SGR (1006) mouse sequences, and asserts the
pane responses against true screen state (pyte tracks the grid, so
partial renderer updates don't matter). Covers spec §11: wheel scroll,
commit click, double-click, file click, divider drag, --no-mouse.

Known input-layer quirk (upstream bubbletea/ultraviolet, observed
2026-11): the first mouse event after launch is dropped by the ESC
disambiguation logic. The script primes the reader with one arrow tap;
a real user's first wheel tick in a quiet terminal can be lost.

The protocol is terminal-agnostic (SGR in, screen state out), so this
covers what iTerm2/Alacritty speak. The Windows Terminal leg remains a
human check (no Windows host).

Needs: pyte (pip install pyte).

Usage:  python3 scripts/mouse-verify.py [path-to-squint-binary]
Exit 0 iff every gesture passes.
"""

import fcntl
import os
import pty
import re
import select
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import time

import pyte

W, H = 120, 40
screen = pyte.Screen(W, H)
stream = pyte.ByteStream(screen)
buf = b""  # raw bytes since the last marker (for byte-level checks)


def drain(seconds=0.8):
    """Read the pty, feeding every byte into the screen tracker."""
    global buf
    end = time.time() + seconds
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.05)
        if r:
            try:
                chunk = os.read(fd, 65536)
            except OSError:
                break
            buf += chunk
            stream.feed(chunk)


def display():
    """The visible screen as a list of rows (0-based)."""
    return [line.rstrip() for line in screen.display]


def find_row(snippet):
    """0-based screen row of the first line containing snippet."""
    for i, line in enumerate(display()):
        if snippet in line:
            return i
    return None


def gauge():
    """The hints bar's scroll percentage (right-aligned row H-1)."""
    m = re.findall(r"(\d+)%", display()[H - 1])
    return int(m[-1]) if m else None


def sgr_click(x, y):
    return f"\x1b[<0;{x};{y}M".encode() + f"\x1b[<0;{x};{y}m".encode()


def sgr_double(x, y):
    return sgr_click(x, y) + sgr_click(x, y)


def sgr_wheel_down(x, y):
    return f"\x1b[<65;{x};{y}M".encode()


def sgr_drag(x0, y0, x1, y1):
    # A drag is a plain button-1 press, motion-flagged events while
    # held, then a release.
    seq = f"\x1b[<0;{x0};{y0}M".encode()
    steps = max(abs(x1 - x0), 1)
    for i in range(1, steps + 1):
        x = x0 + (x1 - x0) * i // steps
        seq += f"\x1b[<32;{x};{y0}M".encode()
    seq += f"\x1b[<0;{x1};{y0}m".encode()
    return seq


def make_demo_repo():
    root = tempfile.mkdtemp(prefix="squint-mouse-")
    env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@t",
               GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@t")

    def git(*args):
        subprocess.run(["git", *args], cwd=root, env=env, check=True,
                       capture_output=True)

    git("init", "-q", ".")
    body = "".join(f"line {i:02d}\n" for i in range(1, 61))
    with open(os.path.join(root, "f1.txt"), "w") as f:
        f.write("first\n" + body)
    with open(os.path.join(root, "f2.txt"), "w") as f:
        f.write("MARKER-ZEBRA\nsecond file\n")
    git("add", "-A")
    git("commit", "-q", "-m", "first: two files")
    with open(os.path.join(root, "f1.txt"), "w") as f:
        f.write(body)
    git("add", "-A")
    git("commit", "-q", "-m", "drop first line")
    return root


def main():
    global fd
    binary = sys.argv[1] if len(sys.argv) > 1 else shutil.which("squint")
    if not binary:
        print("usage: mouse-verify.py [squint-binary]")
        return 2
    repo = make_demo_repo()
    pid, fd = pty.fork()
    if pid == 0:
        os.environ["TERM"] = "xterm-256color"
        os.chdir(repo)
        os.execv(binary, [binary, "-n", "5"])
        os._exit(1)

    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", H, W, 0, 0))
    drain(2.0)
    checks = []

    # Prime the input reader (see module docstring).
    os.write(fd, b"\x1b[B")
    drain(0.5)

    # -- gesture 1: wheel scrolls the pane under the cursor -------------
    # Focus the detail pane ("3") so the gauge shows its scroll, then
    # wheel down over the detail pane (right half, mid height).
    os.write(fd, b"3")
    drain(0.5)
    before = gauge()
    os.write(fd, sgr_wheel_down(90, 20))
    drain(0.6)
    after = gauge()
    checks.append(("wheel scrolls the pane under the cursor",
                   before is not None and after is not None and after > before))

    # -- gesture 2: click a commit row selects it ------------------------
    # Selection starts on HEAD ("drop first line", display row 1, since
    # the priming arrow moved it off index 0). Empirical calibration:
    # an SGR y of R+2 lands on display row R in this harness (decoder
    # offset), so clicking "first: two files" (display row 2) uses y=4.
    os.write(fd, sgr_click(20, 4))
    drain(1.2)
    text = "\n".join(display())
    ok = text.count("first: two files") >= 2  # list row + detail header
    checks.append(("click on a commit row selects it", ok))

    # -- gesture 3: double-click HEAD (display row 1 → y=3); the hints
    # bar then names the detail pane's keys.
    os.write(fd, sgr_double(20, 3))
    drain(1.0)
    hints = display()[H - 1]
    ok = "( / )" in hints or "hunk" in hints
    checks.append(("double-click focuses the detail pane", ok))

    # -- gesture 4: click a file row scrolls the detail to its hunk ------
    # f2.txt belongs to "first: two files", so click back to that commit
    # first (its fanout must complete: the files pane lists f2.txt),
    # wheel the detail past f2's hunk, then click the f2.txt row in the
    # files pane and the marker must reappear.
    os.write(fd, sgr_click(20, 4))
    for _ in range(20):
        drain(0.3)
        if "f2.txt" in "\n".join(display()):
            break
    for _ in range(40):
        if "MARKER-ZEBRA" not in "\n".join(display()):
            break
        os.write(fd, sgr_wheel_down(90, 20))
        drain(0.3)
    drain(0.5)
    gone = "MARKER-ZEBRA" not in "\n".join(display())
    f2y = find_row("f2.txt")
    ok = False
    if gone and f2y is not None:
        x = display()[f2y].find("f2.txt") + 1
        for _ in range(3):  # retry: fanout timing can swallow a pass
            os.write(fd, sgr_click(x + 2, f2y + 1))
            drain(1.2)
            if "MARKER-ZEBRA" in "\n".join(display()):
                ok = True
                break
    checks.append(("file click scrolls the detail to the file's hunk", ok))

    # -- gesture 5: drag the divider resizes the split -------------------
    # The divider sits just left of the files pane; the files title's
    # column moves with it.
    title_col = None
    for line in display():
        j = line.find("files (")
        if j >= 0:
            title_col = j
            break
    ok = False
    if title_col:
        divider_x = title_col - 2  # 1-based SGR column of the border
        os.write(fd, sgr_drag(divider_x, 5, divider_x - 8, 5))
        drain(1.0)
        new_col = None
        for line in display():
            j = line.find("files (")
            if j >= 0:
                new_col = j
                break
        ok = new_col is not None and new_col < title_col - 4
    checks.append(("divider drag resizes the split", ok))

    # -- gesture 6: clean quit -------------------------------------------
    os.write(fd, b"q")
    drain(0.8)
    _, status = os.waitpid(pid, 0)
    checks.append(("clean quit", os.waitstatus_to_exitcode(status) == 0))

    # -- gesture 7: --no-mouse disables tracking entirely -----------------
    pid3, fd3 = pty.fork()
    if pid3 == 0:
        os.environ["TERM"] = "xterm-256color"
        os.chdir(repo)
        os.execv(binary, [binary, "--no-mouse", "-n", "2"])
        os._exit(1)
    fcntl.ioctl(fd3, termios.TIOCSWINSZ, struct.pack("HHHH", H, W, 0, 0))
    out = b""
    end = time.time() + 1.5
    while time.time() < end:
        r, _, _ = select.select([fd3], [], [], 0.05)
        if r:
            try:
                out += os.read(fd3, 65536)
            except OSError:
                break
    mouse_enabled = b"\x1b[?1002h" in out or b"\x1b[?1006h" in out
    os.write(fd3, b"q")
    time.sleep(0.4)
    _, st3 = os.waitpid(pid3, 0)
    checks.append(("--no-mouse disables tracking, quits cleanly",
                   not mouse_enabled and os.waitstatus_to_exitcode(st3) == 0))

    shutil.rmtree(repo, ignore_errors=True)

    passed = 0
    for name, ok in checks:
        print(("PASS " if ok else "FAIL ") + name)
        passed += bool(ok)
    print(f"{passed}/{len(checks)} gestures pass")
    return 0 if passed == len(checks) else 1


if __name__ == "__main__":
    sys.exit(main())
