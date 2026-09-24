#!/usr/bin/env python3
# Test del ripristino dello schermo (replay del buffer di output):
#  - al reattach il contenuto precedente ricompare SENZA che il programma
#    debba ridisegnarsi (era il bug «schermo vuoto dopo il detach»);
#  - al cambio di finestra il contenuto di ciascuna finestra viene conservato;
#  - il demone scrive un file di log accanto al socket.
import os, sys, pty, time, select, struct, fcntl, termios, subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
MTERM = os.path.join(HERE, "..", "mterm")
XRT = os.environ.get("XDG_RUNTIME_DIR") or "/tmp/mterm-test-xrt"
os.makedirs(XRT, exist_ok=True)
ENV = dict(os.environ, XDG_RUNTIME_DIR=XRT, TERM="xterm-256color", SHELL="/bin/sh")

NAME = "t5replay"


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


mrun(["kill", NAME])  # eventuale residuo di corse precedenti

print("=== 1) creo la sessione e produco output riconoscibile ===")
pid, fd = spawn(["new", NAME])
rd(fd, 1.0)
os.write(fd, b"echo REPLAY$((40+2))MARK\r")
out = rd(fd, 1.0)
check(b"REPLAY42MARK" in out, "il marcatore appare nella prima attach")

print("=== 2) detach: il processo client termina, la sessione resta ===")
os.write(fd, b"\x01d")
out = rd(fd, 1.0)
check(b"[staccato]" in out, "il client conferma il detach")
os.waitpid(pid, 0)
check(NAME in mrun(["ls"]), "la sessione sopravvive al detach")

print("=== 3) reattach: lo schermo viene ripristinato dal buffer ===")
# La shell è ferma: non produrrà alcun output da sola. Il contenuto deve
# arrivare dal replay del demone, non da un ridisegno del programma.
pid, fd = spawn(["attach", NAME])
out = rd(fd, 1.5)
check(b"REPLAY42MARK" in out, "il vecchio output ricompare al reattach (replay)")
os.write(fd, b"echo VIVO$((5+5))\r")
out = rd(fd, 1.0)
check(b"VIVO10" in out, "la shell è ancora interattiva dopo il reattach")

print("=== 4) cambio finestra: il contenuto di ogni finestra si conserva ===")
os.write(fd, b"\x01c")  # nuova finestra (1)
rd(fd, 0.8)
os.write(fd, b"echo FIN$((100+1))DUE\r")
out = rd(fd, 1.0)
check(b"FIN101DUE" in out, "la finestra 1 funziona")
os.write(fd, b"\x010")  # torno alla finestra 0
out = rd(fd, 1.2)
check(b"REPLAY42MARK" in out or b"VIVO10" in out, "tornando alla 0 il suo contenuto ricompare")
os.write(fd, b"\x011")  # di nuovo alla finestra 1
out = rd(fd, 1.2)
check(b"FIN101DUE" in out, "tornando alla 1 il suo contenuto ricompare")

print("=== 5) il demone tiene un log accanto al socket ===")
logp = os.path.join(XRT, "mterm", NAME + ".log")
check(os.path.exists(logp), "il file di log esiste")
if os.path.exists(logp):
    logtxt = open(logp).read()
    check("demone avviato" in logtxt, "il log registra l'avvio del demone")
    check("client attaccato" in logtxt, "il log registra gli attach")

print("=== 6) pulizia ===")
os.write(fd, b"\x01d")
rd(fd, 0.8)
os.waitpid(pid, 0)
check("terminata" in mrun(["kill", NAME]), "kill della sessione di test")

print()
print("RISULTATO:", "TUTTO OK" if not fails else f"{len(fails)} FALLIMENTI: {fails}")
sys.exit(1 if fails else 0)
