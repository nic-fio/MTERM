#!/usr/bin/env python3
"""Controlla che i manuali siano allineati al codice. Eseguito da 'make test'.

Fallisce se:
  - un comando o un tasto della guida integrata (helpSections in help.go) non
    ha la sua voce nel riferimento del manuale utente (attributo data-help
    identico al testo della guida), o il manuale documenta una voce che non
    esiste;
  - un file .go manca dalla mappa dei file del manuale tecnico, o il numero
    di righe indicato si scosta di più del 10% da quello vero;
  - un test di tests/ manca dall'elenco dei test del manuale tecnico;
  - l'eseguibile ./mterm (registrato nel repository) manca, non è un ELF
    x86-64 o non contiene la versione di help.go;
  - la versione di help.go non coincide con quella di README.md,
    docs/index.html e dei due manuali;
  - un link interno #ancora dei manuali punta a un id inesistente.
"""
import html
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"
USER = DOCS / "manuale-utente.html"
TECH = DOCS / "manuale-tecnico.html"

errors = []


def err(msg):
    errors.append(msg)


help_go = (ROOT / "help.go").read_text()
user = USER.read_text()
tech = TECH.read_text()

# ---- comandi e tasti ----
sections = help_go[help_go.index("var helpSections"):help_go.index("type helpExample")]
help_items = set()
for spec in re.findall(r'\{"((?:[^"\\]|\\.)+)",\s*"', sections):
    help_items.add(spec.replace('\\"', '"'))

ref = user[user.index('id="riferimento"'):user.index('id="file-usati"')]
doc_items = set(html.unescape(x) for x in re.findall(r'<dt[^>]*\bdata-help="([^"]+)"', ref))

for f in sorted(help_items - doc_items):
    err(f"manuale utente: manca «{f}» nel riferimento")
for f in sorted(doc_items - help_items):
    err(f"manuale utente: «{f}» non esiste nella guida integrata")

# ---- mappa dei file ----
fmap = tech[tech.index('id="file-map"'):]
fmap = fmap[:fmap.index("</table>")]
rows = dict(re.findall(r"<tr><td><code>([\w.]+\.go)</code></td><td>(\d+)</td>", fmap))
for go in sorted(p.name for p in ROOT.glob("*.go")):
    real = len((ROOT / go).read_text().splitlines())
    if go not in rows:
        err(f"manuale tecnico: {go} ({real} righe) manca dalla mappa dei file")
        continue
    doc = int(rows.pop(go))
    if abs(doc - real) > max(3, real // 10):
        err(f"manuale tecnico: {go} ha {real} righe, la mappa dice {doc}")
for go in sorted(rows):
    err(f"manuale tecnico: la mappa cita {go}, che non esiste")

# ---- elenco dei test ----
tlist = tech[tech.index('id="test-list"'):]
tlist = tlist[:tlist.index("</table>")]
documented = set(re.findall(r"<code>(e2e\d*\.py)</code>", tlist))
for t in sorted(p.name for p in (ROOT / "tests").glob("e2e*.py")):
    if t not in documented:
        err(f"manuale tecnico: il test {t} manca dall'elenco dei test")
    documented.discard(t)
for t in sorted(documented):
    err(f"manuale tecnico: l'elenco dei test cita {t}, che non esiste")

# ---- versione ----
m = re.search(r'"mterm (\d+\.\d+(?:\.\d+)?)"', help_go)
version = m.group(1) if m else None
if not version:
    err('help.go: versione "mterm X.Y" non trovata')
else:
    checks = {
        "README.md": rf"Versione {re.escape(version)}\b",
        "docs/index.html": rf"<b>Versione</b> {re.escape(version)}\b",
        "docs/manuale-utente.html": rf"<b>Versione</b> {re.escape(version)}\b",
        "docs/manuale-tecnico.html": rf"<b>Versione</b> {re.escape(version)}\b",
    }
    for rel, pat in checks.items():
        if not re.search(pat, (ROOT / rel).read_text()):
            err(f"{rel}: la versione non è {version} (come in help.go)")

# ---- eseguibile registrato ----
exe = ROOT / "mterm"
if not exe.is_file():
    err("manca l'eseguibile ./mterm: esegui 'make' e registralo")
else:
    data = exe.read_bytes()
    # ELF, 64 bit, x86-64 (e_machine = 0x3E)
    if data[:4] != b"\x7fELF" or data[4] != 2 or data[18:20] != b"\x3e\x00":
        err("./mterm non è un eseguibile Linux x86-64: rigeneralo con 'make'")
    elif version and ("mterm " + version).encode() not in data:
        err(f"./mterm non contiene la versione {version}: rigeneralo con 'make'")

# ---- ancore interne ----
for path, text in ((USER, user), (TECH, tech)):
    ids = set(re.findall(r'\bid="([^"]+)"', text))
    for anchor in sorted(set(re.findall(r'href="#([^"]+)"', text))):
        if anchor not in ids:
            err(f"{path.name}: link a #{anchor}, che non esiste")
    other = TECH if path == USER else USER
    other_ids = set(re.findall(r'\bid="([^"]+)"', other.read_text()))
    for anchor in sorted(set(re.findall(r'href="' + other.name + r'#([^"]+)"', text))):
        if anchor not in other_ids:
            err(f"{path.name}: link a {other.name}#{anchor}, che non esiste")

if errors:
    print("controlli della documentazione: FALLITI")
    for e in errors:
        print("  - " + e)
    sys.exit(1)
print(f"controlli della documentazione: ok ({len(help_items)} comandi e tasti, "
      f"{len(list(ROOT.glob('*.go')))} file, versione {version})")
