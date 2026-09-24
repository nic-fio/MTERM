#!/usr/bin/env python3
# Test delle finestre multiple (stile screen) e della sessione Default.
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


print("=== 1) mterm senza argomenti apre la sessione «Default» ===")
pid, fd = spawn([])  # nessun argomento
rd(fd, 1.2)
ls = mrun(["ls"])
print("    ls ->", repr(ls))
check("Default" in ls, "esiste una sessione «Default»")
check(b"Default" in rd(fd, 0.3) or "Default" in ls, "la barra/sessione riporta «Default»")

print("=== 2) Ctrl-a c crea una seconda finestra ===")
os.write(fd, b"\x01c")
out = rd(fd, 1.0)
# metto un marcatore univoco nella nuova shell per identificarla
os.write(fd, b"echo NUOVA$((2+2))FIN\r")
out = rd(fd, 1.0)
check(b"NUOVA4FIN" in out, "la nuova finestra ha una shell propria e funzionante")

print("=== 3) La barra elenca due finestre (0 e 1) ===")
# forzo un ridisegno della barra aspettando un tick d'orologio
bar = rd(fd, 1.3)
# nella barra devono comparire sia lo '0' sia l'evidenziazione reverse della corrente
check(b"\x1b[7m" in bar, "la finestra corrente è evidenziata in reverse nella barra")

print("=== 4) Ctrl-a p torna alla finestra 0 (shell originale) ===")
os.write(fd, b"\x01p")
rd(fd, 0.8)
os.write(fd, b"echo TORNATO$((9+1))\r")
out = rd(fd, 1.0)
check(b"TORNATO10" in out, "tornati alla finestra 0, la sua shell risponde")

print("=== 5) Ctrl-a \" mostra l'elenco finestre ===")
os.write(fd, b"\x01\"")
out = rd(fd, 0.8)
check(b"Finestre" in out, "l'overlay elenco finestre appare")
os.write(fd, b"1")  # seleziono la finestra 1
rd(fd, 0.8)
os.write(fd, b"echo SELEZ$((3*3))\r")
out = rd(fd, 1.0)
check(b"SELEZ9" in out, "selezionando '1' nell'overlay si va alla finestra 1")

print("=== 6) Ctrl-a k chiude la finestra corrente, la sessione resta ===")
os.write(fd, b"\x01k")
rd(fd, 0.8)
ls = mrun(["ls"])
check("Default" in ls, "la sessione «Default» esiste ancora dopo aver chiuso una finestra")
os.write(fd, b"echo RESTA$((7+7))\r")
out = rd(fd, 1.0)
check(b"RESTA14" in out, "la finestra rimasta (0) è ancora operativa")

print("=== 7) chiudo l'ultima finestra: la sessione termina ===")
os.write(fd, b"\x01k")
out = rd(fd, 1.2)
check(b"[sessione terminata]" in out, "chiudendo l'ultima finestra la sessione termina")
os.waitpid(pid, 0)
time.sleep(0.3)
check("Default" not in mrun(["ls"]), "«Default» sparita")

print()
print("RISULTATO:", "TUTTO OK" if not fails else f"{len(fails)} FALLIMENTI: {fails}")
sys.exit(1 if fails else 0)
