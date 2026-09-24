//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// codici ANSI
const (
	aReset = "\033[0m"
	aBold  = "\033[1m"
	aDim   = "\033[2m"
	aUnder = "\033[4m"
	aCyan  = "\033[36m"
	aGreen = "\033[32m"
	aYel   = "\033[33m"
)

type helpRow struct{ flag, desc string }
type helpSection struct {
	title string
	rows  []helpRow
}

var helpSections = []helpSection{
	{"Comandi", []helpRow{
		{"new, n [nome] [-- cmd]", "Crea una nuova sessione (avvia $SHELL, o il comando indicato dopo «--») e vi si attacca. Senza nome ne assegna uno numerico."},
		{"attach, a NOME", "Si riattacca a una sessione esistente. Se un altro terminale era attaccato, viene staccato."},
		{"ls", "Elenca le sessioni attive e ripulisce i socket orfani."},
		{"kill, k NOME", "Termina una sessione e il processo che contiene."},
		{"NOME", "Scorciatoia: se «NOME» esiste ci si attacca, altrimenti viene creata."},
		{"--help, -h", "Mostra questa guida."},
	}},
	{"Tasti (durante una sessione)", []helpRow{
		{"Ctrl-a d", "Stacca la sessione: il terminale torna libero, ma il processo continua a girare in background."},
		{"Ctrl-a c", "Crea una nuova finestra (una nuova shell) e vi passa."},
		{"Ctrl-a n / Ctrl-a spazio", "Passa alla finestra successiva."},
		{"Ctrl-a p", "Passa alla finestra precedente."},
		{"Ctrl-a 0..9", "Passa direttamente alla finestra con quel numero."},
		{"Ctrl-a \"", "Mostra l'elenco delle finestre; premi il numero per sceglierne una, Esc per annullare."},
		{"Ctrl-a k", "Chiude la finestra corrente (se è l'ultima, termina la sessione)."},
		{"Ctrl-a s", "Salva il contenuto della finestra corrente nella cartella corrente: mterm-<sessione>-w<id>-<orario>.txt (testo leggibile) e .raw (byte grezzi, con i colori)."},
		{"Ctrl-a Ctrl-a", "Invia un Ctrl-a letterale al programma dentro la sessione."},
	}},
}

type helpExample struct{ cmd, note string }

var helpExamples = []helpExample{
	{"mterm new lavoro", "Crea la sessione «lavoro» e ci entra."},
	{"mterm", "Nessun argomento: apre (o crea) la sessione predefinita «Default»."},
	{"mterm n -- claude", "Avvia direttamente «claude» in una sessione staccabile."},
	{"mterm ls", "Mostra quali sessioni sono attive."},
	{"mterm a lavoro", "Si riattacca alla sessione «lavoro»."},
	{"mterm kill lavoro", "Termina la sessione «lavoro»."},
}

const descrizione = "mterm è un gestore di sessioni di terminale minimale: come screen, tiene " +
	"in vita una o più finestre (shell o programmi) dentro pseudo-terminali a cui ci si può " +
	"staccare e riattaccare, ma senza emularne il terminale. I byte passano grezzi tra il tuo " +
	"terminale e la sessione, così TERM, i colori a 24 bit e le sequenze moderne restano intatti — " +
	"il motivo per cui strumenti a tutto schermo funzionano dentro mterm come funzionerebbero fuori. " +
	"Una sessione può contenere più finestre, con una barra di stato sull'ultima riga che le elenca. " +
	"Lanciato senza argomenti, mterm apre (o crea) la sessione predefinita «Default». Prodotto come " +
	"singolo binario statico, senza dipendenze."

func col(color bool, code, s string) string {
	if !color {
		return s
	}
	return code + s + aReset
}

// threeCol compone una riga a tre colonne come le testate delle pagine man.
func threeCol(left, mid, right string, width int) string {
	if width < len(left)+len(mid)+len(right)+2 {
		width = len(left) + len(mid) + len(right) + 2
	}
	gapTot := width - len(left) - len(mid) - len(right)
	l := gapTot / 2
	r := gapTot - l
	return left + strings.Repeat(" ", l) + mid + strings.Repeat(" ", r) + right
}

// wrapText manda a capo il testo a una certa larghezza, con rientro.
func wrapText(s string, width, indent int) []string {
	pad := strings.Repeat(" ", indent)
	avail := width - indent
	if avail < 24 {
		avail = 24
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) <= avail:
			cur += " " + w
		default:
			lines = append(lines, pad+cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, pad+cur)
	}
	return lines
}

// buildHelp produce la pagina di manuale, con o senza colori ANSI.
func buildHelp(color bool, width int) string {
	if width < 60 {
		width = 60
	}
	if width > 100 {
		width = 100
	}
	const ti = 7  // rientro corpo sezione
	const di = 14 // rientro descrizioni

	var b strings.Builder
	w := func(s string) { b.WriteString(s) }
	hdr := func(s string) { w("\n" + col(color, aBold, strings.ToUpper(s)) + "\n") }
	sub := func(s string) { w("\n" + strings.Repeat(" ", ti) + col(color, aBold+aYel, s) + "\n") }
	para := func(s string, ind int) {
		for _, l := range wrapText(s, width, ind) {
			w(l + "\n")
		}
	}
	tp := func(tag, desc string) {
		w(strings.Repeat(" ", ti) + col(color, aBold+aGreen, tag) + "\n")
		para(desc, di)
	}

	w(col(color, aBold, threeCol("MTERM(1)", "Manuale di mterm", "MTERM(1)", width)) + "\n")

	hdr("Nome")
	para("mterm — mini gestore di sessioni di terminale (detach/reattach) senza emulazione", ti)

	hdr("Sintassi")
	syn := func(parts ...string) {
		w(strings.Repeat(" ", ti) + col(color, aBold, "mterm") + " ")
		for i, p := range parts {
			if i > 0 {
				w(" ")
			}
			w(col(color, aUnder, p))
		}
		w("\n")
	}
	syn("new", "[nome]")
	syn("attach", "nome")
	syn("ls")
	syn("kill", "nome")

	hdr("Descrizione")
	para(descrizione, ti)

	hdr("Opzioni")
	for _, sec := range helpSections {
		sub(sec.title)
		for _, r := range sec.rows {
			tp(r.flag, r.desc)
		}
	}

	hdr("Esempi")
	for _, ex := range helpExamples {
		tp(ex.cmd, ex.note)
	}

	hdr("File")
	tp("$XDG_RUNTIME_DIR/mterm/NOME.sock", "Socket di controllo della sessione (fallback: /tmp/mterm-UID/).")
	tp("$XDG_RUNTIME_DIR/mterm/NOME.log", "Log del demone della sessione: avvio, attach e detach, errori.")

	hdr("Ambiente")
	tp("SHELL", "Shell avviata dalle nuove sessioni (default: /bin/sh).")
	tp("TERM", "Passato inalterato al programma nella sessione: nessuna emulazione.")
	tp("PAGER", "Pager usato per impaginare questa guida (default: less).")
	tp("NO_COLOR", "Se impostata, disabilita i colori.")

	hdr("Stato di uscita")
	tp("0", "Completato con successo.")
	tp("1", "Errore durante l'esecuzione.")

	hdr("Vedere anche")
	para(col(color, aBold, "screen")+"(1), "+col(color, aBold, "tmux")+"(1)", ti)

	w("\n" + col(color, aBold, threeCol("mterm 1.0", "", "MTERM(1)", width)) + "\n")
	return b.String()
}

var reANSI = regexp.MustCompile(`\033\[[0-9;]*m`)

func stripANSI(s string) string { return reANSI.ReplaceAllString(s, "") }

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// termWidth rileva la larghezza del terminale, con fallback a 80.
func termWidth() int {
	w, _ := termSize()
	if w <= 0 {
		return 80
	}
	return w
}

// displayHelp mostra la guida: TUI interattiva in un terminale, altrimenti
// pager man-style, altrimenti testo semplice.
func displayHelp() {
	if !isTTY(os.Stdout) || !isTTY(os.Stdin) {
		fmt.Print(stripANSI(buildHelp(false, 80)))
		return
	}
	if err := runTUI(); err == nil {
		return
	}
	color := os.Getenv("NO_COLOR") == ""
	text := buildHelp(color, termWidth())
	cmd := pagerCommand()
	if cmd == nil {
		fmt.Print(text)
		return
	}
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Print(text)
	}
}

func pagerCommand() *exec.Cmd {
	if p := os.Getenv("PAGER"); p != "" {
		return exec.Command("sh", "-c", p)
	}
	if p, err := exec.LookPath("less"); err == nil {
		return exec.Command(p, "-R", "-F", "-X")
	}
	if p, err := exec.LookPath("more"); err == nil {
		return exec.Command(p)
	}
	return nil
}
