#!/usr/bin/env python3
# Test end-to-end delle proprietà distintive di mterm: pass-through truecolor,
# riga riservata alla barra, output dal vivo al reattach, sfratto multi-attach.
# Uso: python3 tests/e2e2.py
import os, sys, pty, time, select, struct, fcntl, termios, subprocess, re

HERE = os.path.dirname(os.path.abspath(__file__))
MTERM = os.path.join(HERE, "..", "mterm")
XRT = os.environ.get("XDG_RUNTIME_DIR") or "/tmp/mterm-test-xrt"
os.makedirs(XRT, exist_ok=True)
ENV = dict(os.environ, XDG_RUNTIME_DIR=XRT, TERM="xterm-256color", SHELL="/bin/sh")


def sws(fd, r, c):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", r, c, 0, 0))


def rd(fd, s):
    b = b""
    e = time.time() + s
    while time.time() < e:
        r, _, _ = select.select([fd], [], [], 0.1)
        if r:
            try:
                d = os.read(fd, 65536)
            except OSError:
                break
            if not d:
                break
            b += d
    return b


def spawn(args, rows=30, cols=100):
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(MTERM, [MTERM] + args, ENV)
        os._exit(127)
    sws(fd, rows, cols)
    return pid, fd


def mrun(a):
    return subprocess.run([MTERM] + a, env=ENV, capture_output=True, text=True).stdout.strip()


fails = []


def check(c, m):
    print(("  OK  " if c else " FAIL ") + m)
    if not c:
        fails.append(m)


print("=== 1) Truecolor pass-through (24-bit, verbatim) ===")
pid, fd = spawn(["new", "c1"])
rd(fd, 1.0)
os.write(fd, b"printf '\\033[38;2;17;34;51mZ\\033[0m\\n'\r")
out = rd(fd, 1.0)
check(b"\x1b[38;2;17;34;51m" in out, "sequenza truecolor arriva intatta (screen la distruggerebbe)")
os.write(fd, b"\x01d"); rd(fd, 0.6); os.waitpid(pid, 0)

print("=== 2) L'app vede righe-1 (una riga riservata alla barra) ===")
pid, fd = spawn(["attach", "c1"], rows=30, cols=100)
rd(fd, 0.8)
os.write(fd, b"stty size\r")
out = rd(fd, 1.0)
check(b"29 100" in out, "stty size dentro la sessione riporta 29x100 (30-1 righe)")
os.write(fd, b"\x01d"); rd(fd, 0.6); os.waitpid(pid, 0)
mrun(["kill", "c1"])

print("=== 3) Output dal vivo dopo il reattach ('vedo il programma lavorare') ===")
pid, fd = spawn(["new", "c2", "--", "sh", "-c", "i=0; while true; do i=$((i+1)); echo TICK$i; sleep 0.25; done"])
rd(fd, 0.8)
os.write(fd, b"\x01d"); rd(fd, 0.6); os.waitpid(pid, 0)
time.sleep(1.0)  # il programma continua a girare mentre siamo staccati
pid, fd = spawn(["attach", "c2"])
out = rd(fd, 1.2)
check(len(re.findall(rb"TICK(\d+)", out)) > 0, "nuovi TICK arrivano dopo il reattach (output dal vivo)")
os.write(fd, b"\x01d"); rd(fd, 0.6); os.waitpid(pid, 0)
mrun(["kill", "c2"])

print("=== 4) Multi-attach: il secondo attach sfratta il primo ===")
pid1, fd1 = spawn(["new", "c3"]); rd(fd1, 0.9)
pid2, fd2 = spawn(["attach", "c3"]); time.sleep(0.4)
out1 = rd(fd1, 1.2)
check(b"ripresa altrove" in out1, "il primo client riceve '[staccato: sessione ripresa altrove]'")
os.waitpid(pid1, 0)
os.write(fd2, b"\x01d"); rd(fd2, 0.6); os.waitpid(pid2, 0)
mrun(["kill", "c3"])

print()
print("RISULTATO:", "TUTTO OK" if not fails else f"{len(fails)} FALLIMENTI: {fails}")
sys.exit(1 if fails else 0)
