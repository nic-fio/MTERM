#!/usr/bin/env python3
# Test end-to-end del ciclo detach/reattach di mterm, guidato da una pty reale.
# Uso: python3 tests/e2e.py   (dopo aver compilato ./mterm con `make`)
import os, sys, pty, time, select, struct, fcntl, termios, subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
MTERM = os.path.join(HERE, "..", "mterm")
XRT = os.environ.get("XDG_RUNTIME_DIR") or "/tmp/mterm-test-xrt"
os.makedirs(XRT, exist_ok=True)
ENV = dict(os.environ, XDG_RUNTIME_DIR=XRT, TERM="xterm-256color", SHELL="/bin/sh")


def set_winsize(fd, rows, cols):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))


def read_for(fd, secs):
    buf = b""
    end = time.time() + secs
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if r:
            try:
                d = os.read(fd, 65536)
            except OSError:
                break
            if not d:
                break
            buf += d
    return buf


def spawn(args):
    pid, fd = pty.fork()
    if pid == 0:  # figlio
        os.execve(MTERM, [MTERM] + args, ENV)
        os._exit(127)
    set_winsize(fd, 30, 100)
    return pid, fd


def mrun(args):
    return subprocess.run([MTERM] + args, env=ENV, capture_output=True, text=True).stdout.strip()


fails = []


def check(cond, msg):
    print(("  OK  " if cond else " FAIL ") + msg)
    if not cond:
        fails.append(msg)


print("=== A) new t1 + attach ===")
pid, fd = spawn(["new", "t1"])
out = read_for(fd, 1.5)
check(b"\x1b[1;37;42m" in out, "barra di stato con SGR verde/bianco/grassetto")
check(b"t1" in out, "barra mostra nome sessione 't1'")

print("=== B) eseguo un comando dentro la sessione ===")
# l'output (A42Z) non compare nell'input echeggiato: distingue una shell vera
# dall'eco della line-discipline della pty.
os.write(fd, b"echo A$((21+21))Z\r")
out = read_for(fd, 1.2)
check(b"A42Z" in out, "una shell vera valuta il comando (A42Z)")

print("=== C) detach con Ctrl-a d ===")
os.write(fd, b"\x01d")
out = read_for(fd, 1.5)
check(b"[staccato]" in out, "messaggio [staccato]")
os.waitpid(pid, 0)

print("=== D) la sessione sopravvive al detach ===")
ls = mrun(["ls"])
print("    ls ->", repr(ls))
check("t1" in ls, "t1 ancora attiva dopo il detach")

print("=== E) reattach e verifica shell viva ===")
pid, fd = spawn(["attach", "t1"])
out = read_for(fd, 1.0)
os.write(fd, b"echo B$((50+50))C\r")
out = read_for(fd, 1.2)
check(b"B100C" in out, "la stessa shell risponde dopo il reattach (B100C)")

print("=== F) chiudo la shell: la sessione termina ===")
os.write(fd, b"exit\r")
out = read_for(fd, 1.5)
check(b"[sessione terminata]" in out, "messaggio [sessione terminata] quando la shell esce")
os.waitpid(pid, 0)
time.sleep(0.3)
ls = mrun(["ls"])
print("    ls ->", repr(ls))
check("t1" not in ls, "t1 sparita dopo l'uscita della shell")

print()
print("RISULTATO:", "TUTTO OK" if not fails else f"{len(fails)} FALLIMENTI: {fails}")
sys.exit(1 if fails else 0)
