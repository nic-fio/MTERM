#!/usr/bin/env python3
# Test dell'igiene dei modi DEC privati (bug «caratteri a caso muovendo il mouse»):
#  - se una finestra abilita il mouse tracking, passando a un'altra finestra il
#    demone lo spegne (altrimenti il movimento del mouse diventa input casuale);
#  - tornando alla finestra che lo aveva acceso, il modo viene riaffermato PRIMA
#    del replay (quindi dallo stato tracciato, anche se il buffer fosse tagliato);
#  - una sequenza spezzata tra due letture della PTY viene comunque riconosciuta;
#  - al detach il client spegne mouse/paste/focus e rimostra il cursore.
import os, sys, pty, time, select, struct, fcntl, termios, subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
MTERM = os.path.join(HERE, "..", "mterm")
XRT = os.environ.get("XDG_RUNTIME_DIR") or "/tmp/mterm-test-xrt"
os.makedirs(XRT, exist_ok=True)
ENV = dict(os.environ, XDG_RUNTIME_DIR=XRT, TERM="xterm-256color", SHELL="/bin/sh")

NAME = "t6mouse"

CLEAR = b"\x1b[H\x1b[2J"  # il repaint del demone: i modi vengono emessi PRIMA


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


def prefix_of(burst):
    """La parte del burst che precede il clear del repaint (i modi asseriti)."""
    i = burst.find(CLEAR)
    return burst[:i] if i >= 0 else b""


mrun(["kill", NAME])  # eventuale residuo di corse precedenti

print("=== 1) finestra 0: una «TUI» accende il mouse tracking (sequenza spezzata) ===")
pid, fd = spawn(["new", NAME])
rd(fd, 1.0)
# La sequenza ESC[?1003h arriva spezzata in due scritture sulla PTY: il demone
# deve ricomporla tramite la coda conservata tra una lettura e l'altra.
os.write(fd, b"printf '\\033[?10'; sleep 0.3; printf '03h\\033[?1006h'; echo MDONE\r")
out = rd(fd, 1.5)
check(b"MDONE" in out, "il comando che accende il mouse è stato eseguito")

print("=== 2) cambio a una finestra nuova: il mouse viene spento ===")
os.write(fd, b"\x01c")  # nuova finestra (1)
out = rd(fd, 1.2)
pre = prefix_of(out)
check(b"\x1b[?1003l" in pre, "il tracking del movimento viene spento prima del repaint")
check(b"\x1b[?1006l" in pre, "anche la codifica SGR viene spenta")
check(b"\x1b[?1049l" in pre, "lo stato schermo-alternato viene riaffermato (spento)")

print("=== 3) ritorno alla finestra 0: il mouse viene riacceso dallo stato tracciato ===")
os.write(fd, b"\x010")
out = rd(fd, 1.2)
pre = prefix_of(out)
check(b"\x1b[?1003h" in pre, "il tracking torna acceso PRIMA del replay (stato tracciato)")
check(b"\x1b[?1006h" in pre, "la codifica SGR torna accesa")

print("=== 4) la «TUI» spegne il mouse: il nuovo stato viene ricordato ===")
os.write(fd, b"printf '\\033[?1003l\\033[?1006l'; echo MOFF\r")
out = rd(fd, 1.0)
check(b"MOFF" in out, "il comando che spegne il mouse è stato eseguito")
os.write(fd, b"\x011")  # via dalla finestra 0...
rd(fd, 1.0)
os.write(fd, b"\x010")  # ...e ritorno
out = rd(fd, 1.2)
pre = prefix_of(out)
check(b"\x1b[?1003l" in pre and b"\x1b[?1003h" not in pre,
      "tornando alla 0 il tracking resta spento (lo spegnimento è stato tracciato)")

print("=== 5) detach: il client spegne tutto nel terminale dell'utente ===")
os.write(fd, b"\x01d")
out = rd(fd, 1.0)
check(b"[staccato]" in out, "il client conferma il detach")
check(b"\x1b[?1003l" in out, "al detach il mouse tracking viene spento")
check(b"\x1b[?2004l" in out, "al detach il bracketed paste viene spento")
check(b"\x1b[?25h" in out, "al detach il cursore torna visibile")
os.waitpid(pid, 0)

print("=== 6) pulizia ===")
check("terminata" in mrun(["kill", NAME]), "kill della sessione di test")

print()
print("RISULTATO:", "TUTTO OK" if not fails else f"{len(fails)} FALLIMENTI: {fails}")
sys.exit(1 if fails else 0)
