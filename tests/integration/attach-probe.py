#!/usr/bin/env python3
"""Attach to the isolated minimal-image fixture, then detach without killing it."""

import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import time


project, *argv = sys.argv[1:]
if not argv:
    raise SystemExit("usage: attach-probe.py fixture-container launcher [args...]")

tmux_command = ["docker", "exec", project]
runtime_uid = os.environ.get("HCORRAL_TEST_TMUX_UID")
if runtime_uid:
    if not runtime_uid.isdecimal():
        raise SystemExit("HCORRAL_TEST_TMUX_UID must be numeric")
    tmux_command.extend(["gosu", runtime_uid])
tmux_command.append("tmux")

pid, fd = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
    env = dict(os.environ, TERM="xterm-256color")
    env.pop("TMUX", None)
    env.pop("TMUX_PANE", None)
    os.execvpe(argv[0], argv, env)

output = bytearray()
deadline = time.monotonic() + 60
detached = False
reaped = False
try:
    while time.monotonic() < deadline:
        if select.select([fd], [], [], 0.1)[0]:
            try:
                data = os.read(fd, 65536)
            except OSError:
                data = b""
            output.extend(data)
            del output[:-65536]
            if b"\x1b[c" in data or b"\x1b[0c" in data:
                os.write(fd, b"\x1b[?1;2c")
            if b"\x1b[>c" in data or b"\x1b[>0c" in data:
                os.write(fd, b"\x1b[>0;95;0c")
        exited, status = os.waitpid(pid, os.WNOHANG)
        if exited:
            reaped = True
            if not detached or os.waitstatus_to_exitcode(status) != 0:
                raise RuntimeError(f"launcher exited before clean detach (status {status})")
            break
        if not detached:
            clients = subprocess.run(
                tmux_command + ["list-clients", "-F", "#{session_name}"],
                capture_output=True, timeout=5, check=False,
            )
            if clients.returncode == 0 and b"hcorral" in clients.stdout.splitlines():
                subprocess.run(
                    tmux_command + ["detach-client", "-s", "hcorral"],
                    capture_output=True, timeout=5, check=True,
                )
                detached = True
                deadline = min(deadline, time.monotonic() + 10)
    else:
        raise RuntimeError("launcher did not attach and detach within the deadline")
except Exception:
    sys.stderr.buffer.write(output)
    raise
finally:
    if not reaped:
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        os.waitpid(pid, 0)
    os.close(fd)
