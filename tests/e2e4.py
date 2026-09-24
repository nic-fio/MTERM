#!/usr/bin/env python3
# Verifica che la barra non venga "sporcata" quando un programma resetta la
# regione di scorrimento (tipico all'uscita): il client deve riaffermare la
# regione [1..righe-1] sullo schermo normale, ma NON su quello alternato.
import os, sys, pty, time, select, struct, fcntl, termios, subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
MTERM = os.path.join(HERE, "..", "mterm")
XRT = os.environ.get("XDG_RUNTIME_DIR") or "/tmp/mterm-test-xrt"
os.makedirs(XRT, exist_ok=True)
ENV = dict(os.environ, XDG_RUNTIME_DIR=XRT, TERM="xterm-256color", SHELL="/bin/sh")

REGION = b"\x1b[1;29r"  # regione [1..29] su un terminale di 30 righe (29 = righe-1)


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


def mrun(a):
    return subprocess.run([MTERM] + a, env=ENV, capture_output=True, text=True).stdout.strip()


fails = []


def check(c, m):
    print(("  OK  " if c else " FAIL ") + m)
    if not c:
        fails.append(m)


pid, fd = pty.fork()
if pid == 0:
    os.execve(MTERM, [MTERM, "new", "t", "sh"], ENV)  # (sh esplicito, ininfluente)
    os._exit(127)
sws(fd, 30, 100)
rd(fd, 1.0)

print("=== 1) Un programma resetta la regione (\\033[r): il client la riafferma ===")
os.write(fd, b"printf '\\033[r'; echo FATTO\r")
out = rd(fd, 1.3)
check(REGION in out, "sullo schermo normale la regione [1..29] viene riaffermata")

print("=== 2) Su schermo alternato la regione NON viene toccata ===")
os.write(fd, b"printf '\\033[?1049h'\r")
rd(fd, 1.0)  # il demone comunica alt=1 al client
out = rd(fd, 1.6)  # >1 tick d'orologio: eventuali ridisegni barra non devono riaffermare la regione
check(REGION not in out, "sullo schermo alternato il client non riafferma la regione (non disturba l'app)")

print("=== 3) Tornati allo schermo normale, la regione torna a essere riaffermata ===")
os.write(fd, b"printf '\\033[?1049l'\r")
out = rd(fd, 1.3)
check(REGION in out, "uscendo dallo schermo alternato la regione viene di nuovo riaffermata")

os.write(fd, b"\x01d")
rd(fd, 0.5)
os.waitpid(pid, 0)
mrun(["kill", "t"])

print()
print("RISULTATO:", "TUTTO OK" if not fails else f"{len(fails)} FALLIMENTI: {fails}")
sys.exit(1 if fails else 0)
