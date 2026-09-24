//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// client è un terminale attaccato a una sessione. Fa da tubo grezzo tra il
// terminale e la finestra corrente della sessione; genera in proprio solo la
// barra di stato e l'eventuale overlay di scelta finestra.
type client struct {
	conn net.Conn
	name string

	outMu sync.Mutex // scritture su stdout
	sndMu sync.Mutex // scritture sul socket

	dimMu sync.Mutex
	cols  int
	rows  int // righe fisiche
	bar   bool

	wlMu   sync.Mutex
	wins   []winItem
	curID  int
	curAlt bool // la finestra corrente è su schermo alternato (da metadati)

	ovMu    sync.Mutex
	overlay bool

	flashMu    sync.Mutex
	flashMsg   string    // notifica transitoria mostrata a destra nella barra
	flashUntil time.Time // fino a quando mostrarla

	endOnce sync.Once
	endCh   chan string
	lastOut time.Time // ultimo momento in cui abbiamo scritto output dell'app
}

type winItem struct {
	id    int
	title string
}

type srvEvent struct {
	typ  byte
	data []byte
}

const (
	prefixKey = 0x01            // Ctrl-a
	barSGR    = "\033[1;37;42m" // grassetto, bianco su verde
)

func attach(name string) error {
	path := sockPath(name)
	conn, err := dialMode(path, 'A')
	if err != nil {
		return fmt.Errorf("nessuna sessione «%s» (usa «mterm ls»)", name)
	}

	old, err := enterRaw(0)
	if err != nil {
		conn.Close()
		return fmt.Errorf("terminale non compatibile: %v", err)
	}

	cols, rows := termSize()
	c := &client{
		conn:  conn,
		name:  name,
		cols:  cols,
		rows:  rows,
		bar:   rows >= 3,
		endCh: make(chan string, 4),
	}

	c.out("\033[2J\033[H")
	c.setRegion()
	c.sendResize()
	c.drawBar()

	evCh := make(chan srvEvent, 64)
	go c.readLoop(evCh)
	go c.outputLoop(evCh)
	go c.inputLoop()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, winchSignals...)
	go func() {
		for range winch {
			cols, rows := termSize()
			c.dimMu.Lock()
			c.cols, c.rows = cols, rows
			c.bar = rows >= 3
			c.dimMu.Unlock()
			c.sendResize()
			c.setRegion()
			c.drawBar()
		}
	}()

	reason := <-c.endCh
	signal.Stop(winch)
	c.cleanup()
	restoreTerm(0, old)

	switch reason {
	case "detached":
		fmt.Fprintln(os.Stderr, "[staccato]")
	default:
		if isAlive(path) {
			fmt.Fprintln(os.Stderr, "[staccato: sessione ripresa altrove]")
		} else {
			fmt.Fprintln(os.Stderr, "[sessione terminata]")
		}
	}
	return nil
}

func (c *client) end(reason string) {
	c.endOnce.Do(func() {
		c.endCh <- reason
		c.conn.Close()
	})
}

// --- scritture ---

func (c *client) out(s string) {
	c.outMu.Lock()
	io.WriteString(os.Stdout, s)
	c.outMu.Unlock()
}

func (c *client) outBytes(b []byte) {
	c.outMu.Lock()
	os.Stdout.Write(b)
	c.outMu.Unlock()
}

func (c *client) sendResize() {
	c.dimMu.Lock()
	rows := c.appRows()
	cols := c.cols
	c.dimMu.Unlock()
	var f [5]byte
	f[0] = 'S'
	binary.BigEndian.PutUint16(f[1:3], uint16(rows))
	binary.BigEndian.PutUint16(f[3:5], uint16(cols))
	c.sndMu.Lock()
	c.conn.Write(f[:])
	c.sndMu.Unlock()
}

func (c *client) sendData(b []byte) {
	for len(b) > 0 {
		n := len(b)
		if n > 65535 {
			n = 65535
		}
		var h [3]byte
		h[0] = 'D'
		binary.BigEndian.PutUint16(h[1:3], uint16(n))
		c.sndMu.Lock()
		c.conn.Write(h[:])
		c.conn.Write(b[:n])
		c.sndMu.Unlock()
		b = b[n:]
	}
}

func (c *client) sendCtl(t byte) {
	c.sndMu.Lock()
	c.conn.Write([]byte{t})
	c.sndMu.Unlock()
}

func (c *client) sendGoto(id int) {
	var f [3]byte
	f[0] = 'g'
	binary.BigEndian.PutUint16(f[1:3], uint16(id))
	c.sndMu.Lock()
	c.conn.Write(f[:])
	c.sndMu.Unlock()
}

func (c *client) appRows() int {
	if c.bar {
		return c.rows - 1
	}
	return c.rows
}

// --- cicli ---

func (c *client) readLoop(evCh chan<- srvEvent) {
	hdr := make([]byte, 5)
	for {
		if _, err := io.ReadFull(c.conn, hdr); err != nil {
			close(evCh)
			c.end("session")
			return
		}
		n := binary.BigEndian.Uint32(hdr[1:5])
		var data []byte
		if n > 0 {
			data = make([]byte, n)
			if _, err := io.ReadFull(c.conn, data); err != nil {
				close(evCh)
				c.end("session")
				return
			}
		}
		evCh <- srvEvent{hdr[0], data}
	}
}

// outputLoop riversa l'output sul terminale. Per velocità e pulizia coalizza
// tutti i frame già pronti in un'unica scrittura per burst, e ridisegna la barra
// solo quando il flusso si ferma (più il tick dell'orologio), invece che di
// continuo in mezzo all'output.
func (c *client) outputLoop(evCh <-chan srvEvent) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// timer «a fine burst»: riarmato a ogni output, ridisegna la barra ~12ms
	// dopo che l'output si è fermato.
	barTimer := time.NewTimer(time.Hour)
	if !barTimer.Stop() {
		<-barTimer.C
	}
	armBar := func() {
		if !barTimer.Stop() {
			select {
			case <-barTimer.C:
			default:
			}
		}
		barTimer.Reset(12 * time.Millisecond)
	}

	buf := make([]byte, 0, 64*1024)
	flush := func() {
		if len(buf) > 0 {
			c.outBytes(buf)
			buf = buf[:0]
			c.lastOut = time.Now()
		}
	}
	handle := func(e srvEvent) {
		switch e.typ {
		case 'D':
			if !c.overlayActive() {
				buf = append(buf, e.data...)
			}
		case 'M':
			flush() // preserva l'ordine col flusso dati
			c.setWinlist(e.data)
			c.drawBar() // ridisegna barra e riafferma la regione (secondo lo stato alt)
		case 'B':
			flush() // il buffer è una risposta una-tantum: non tocca lo schermo
			c.saveBuffer(e.data)
		}
	}

	for {
		select {
		case e, ok := <-evCh:
			if !ok {
				flush()
				return
			}
			handle(e)
			// drena tutto ciò che è già in coda: una sola write per burst
		drain:
			for {
				select {
				case e2, ok := <-evCh:
					if !ok {
						flush()
						return
					}
					handle(e2)
				default:
					break drain
				}
			}
			flush()
			armBar()
		case <-barTimer.C:
			if !c.overlayActive() {
				c.drawBar()
			}
		case <-ticker.C:
			// orologio: aggiorna solo se il flusso è fermo, per non iniettare
			// la barra in mezzo a un output continuo.
			if !c.overlayActive() && time.Since(c.lastOut) > 40*time.Millisecond {
				c.drawBar()
			}
		}
	}
}

func (c *client) inputLoop() {
	buf := make([]byte, 4096)
	armed := false
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			c.end("detached")
			return
		}
		out := make([]byte, 0, n)
		flush := func() {
			if len(out) > 0 {
				c.sendData(out)
				out = out[:0]
			}
		}
		for _, ch := range buf[:n] {
			if c.overlayActive() {
				c.handleOverlayKey(ch)
				continue
			}
			if !armed {
				if ch == prefixKey {
					armed = true
					continue
				}
				out = append(out, ch)
				continue
			}
			armed = false
			switch ch {
			case 'd', 'D':
				flush()
				c.end("detached")
				return
			case 'c', 'C':
				flush()
				c.sendCtl('C') // nuova finestra
			case 'n', ' ':
				flush()
				c.sendCtl('n') // successiva
			case 'p':
				flush()
				c.sendCtl('p') // precedente
			case 'k', 'K':
				flush()
				c.sendCtl('x') // chiudi finestra corrente
			case 's', 'S':
				flush()
				c.sendCtl('b') // chiedi il buffer, poi lo salvo su file
			case '"':
				flush()
				c.openOverlay()
			case prefixKey:
				out = append(out, prefixKey) // Ctrl-a letterale
			default:
				if ch >= '0' && ch <= '9' {
					flush()
					c.sendGoto(int(ch - '0'))
				} else {
					out = append(out, prefixKey, ch)
				}
			}
		}
		flush()
	}
}

// --- lista finestre / metadati ---

func (c *client) setWinlist(data []byte) {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	c.wlMu.Lock()
	defer c.wlMu.Unlock()
	c.wins = c.wins[:0]
	if len(lines) == 0 || lines[0] == "" {
		return
	}
	head := strings.SplitN(lines[0], "\t", 2)
	fmt.Sscanf(head[0], "%d", &c.curID)
	c.curAlt = len(head) > 1 && head[1] == "1"
	for _, ln := range lines[1:] {
		parts := strings.SplitN(ln, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		var id int
		fmt.Sscanf(parts[0], "%d", &id)
		c.wins = append(c.wins, winItem{id: id, title: parts[1]})
	}
}

// --- overlay di scelta finestra (Ctrl-a ") ---

func (c *client) overlayActive() bool {
	c.ovMu.Lock()
	defer c.ovMu.Unlock()
	return c.overlay
}

func (c *client) openOverlay() {
	c.ovMu.Lock()
	c.overlay = true
	c.ovMu.Unlock()
	c.drawOverlay()
}

func (c *client) closeOverlay() {
	c.ovMu.Lock()
	c.overlay = false
	c.ovMu.Unlock()
}

func (c *client) handleOverlayKey(ch byte) {
	c.wlMu.Lock()
	cur := c.curID
	ids := map[int]bool{}
	for _, w := range c.wins {
		ids[w.id] = true
	}
	c.wlMu.Unlock()

	switch {
	case ch == 0x1b || ch == 'q': // annulla
		c.closeOverlay()
		c.sendGoto(cur) // forza il repaint della finestra corrente
	case ch >= '0' && ch <= '9':
		id := int(ch - '0')
		c.closeOverlay()
		if ids[id] {
			c.sendGoto(id)
		} else {
			c.sendGoto(cur) // id inesistente: ridisegna e basta
		}
	default:
		// ignora: resta nell'overlay
	}
}

func (c *client) drawOverlay() {
	c.wlMu.Lock()
	wins := append([]winItem(nil), c.wins...)
	cur := c.curID
	c.wlMu.Unlock()

	var b strings.Builder
	b.WriteString("\033[H\033[2J")
	b.WriteString("\r\n  \033[1mFinestre\033[0m  —  premi il numero, Esc per annullare\r\n\r\n")
	for _, w := range wins {
		marker := "   "
		line := fmt.Sprintf("   %d  %s", w.id, w.title)
		if w.id == cur {
			marker = " \033[1m►\033[0m "
			line = fmt.Sprintf(" \033[1m%d  %s\033[0m", w.id, w.title)
		}
		b.WriteString(marker + line + "\r\n")
	}
	c.out(b.String())
	c.drawBar()
}

// --- barra di stato e regione di scorrimento ---

func (c *client) setRegion() {
	c.dimMu.Lock()
	on := c.bar
	ar := c.appRows()
	c.dimMu.Unlock()
	if !on {
		return
	}
	c.out(fmt.Sprintf("\0337\033[1;%dr\0338", ar))
}

func (c *client) drawBar() {
	c.dimMu.Lock()
	on := c.bar
	cols := c.cols
	rows := c.rows
	name := c.name
	c.dimMu.Unlock()
	if !on {
		return
	}
	bar := c.buildBar(name, cols)

	c.wlMu.Lock()
	alt := c.curAlt
	c.wlMu.Unlock()

	// Sullo schermo normale riaffermiamo la regione di scorrimento [1..righe-1]
	// a ogni ridisegno: se un programma l'ha resettata uscendo, qui la
	// ristabiliamo, così la shell non scorre mai sopra la barra. Sullo schermo
	// alternato non la tocchiamo (l'app gestisce la propria regione).
	region := ""
	if !alt {
		region = fmt.Sprintf("\033[1;%dr", rows-1)
	}
	// Synchronized Update Mode (?2026): il terminale rende la barra in modo
	// atomico (no flicker); chi non lo supporta ignora la sequenza. DECSC/DECRC
	// (\0337/\0338) salvano e ripristinano posizione e attributi del cursore.
	c.out(fmt.Sprintf("\033[?2026h\0337%s\033[%d;1H%s%s\033[0m\0338\033[?2026l", region, rows, barSGR, bar))
}

// buildBar compone la barra: a sinistra sessione e lista finestre (la corrente
// evidenziata in reverse), a destra utente@host e orologio. La larghezza
// visibile risultante è esattamente cols.
func (c *client) buildBar(name string, cols int) string {
	c.wlMu.Lock()
	wins := append([]winItem(nil), c.wins...)
	cur := c.curID
	c.wlMu.Unlock()

	var left strings.Builder
	var plain strings.Builder // versione senza SGR, per il fallback stretto
	vis := 0
	add := func(s string) {
		left.WriteString(s)
		plain.WriteString(s)
		vis += utf8.RuneCountInString(s)
	}

	add(fmt.Sprintf(" ⬢ %s ", name))
	for _, w := range wins {
		label := fmt.Sprintf(" %d ", w.id)
		if w.id == cur {
			left.WriteString("\033[7m")
			left.WriteString(label)
			left.WriteString("\033[27m")
			plain.WriteString(label)
			vis += utf8.RuneCountInString(label)
		} else {
			add(label)
		}
	}
	leftVis := vis

	right := fmt.Sprintf(" %s@%s  %s ", shortUser(), shortHost(), time.Now().Format("15:04:05"))
	if f := c.flash(); f != "" {
		right = " " + f + " "
	}
	rightVis := utf8.RuneCountInString(right)

	switch {
	case leftVis+rightVis <= cols:
		return left.String() + strings.Repeat(" ", cols-leftVis-rightVis) + right
	case leftVis <= cols:
		return left.String() + strings.Repeat(" ", cols-leftVis)
	default:
		p := truncRunes(plain.String(), cols)
		return p + strings.Repeat(" ", cols-utf8.RuneCountInString(p))
	}
}

func (c *client) cleanup() {
	c.wlMu.Lock()
	alt := c.curAlt
	c.wlMu.Unlock()
	c.dimMu.Lock()
	on := c.bar
	rows := c.rows
	c.dimMu.Unlock()
	if alt {
		// La finestra era sullo schermo alternato: si torna a quello normale,
		// per non lasciare il terminale dell'utente dentro la schermata
		// dell'app dopo il detach.
		c.out("\033[?1049l")
	}
	// Spegne ciò che l'app nella finestra può aver lasciato acceso nel
	// terminale dell'utente (mouse tracking, focus events, bracketed paste,
	// cursore nascosto): altrimenti dopo il detach ogni movimento del mouse
	// arriverebbe alla shell come caratteri a caso.
	c.out("\033[?9l\033[?1000l\033[?1001l\033[?1002l\033[?1003l\033[?1004l" +
		"\033[?1005l\033[?1006l\033[?1015l\033[?1016l\033[?2004l\033[?25h")
	c.out("\033[0m")
	if on {
		c.out(fmt.Sprintf("\033[r\033[%d;1H\033[K\r\n", rows))
	}
}

// --- salvataggio del buffer (Ctrl-a s) ---

// saveBuffer scrive su file il buffer di replay ricevuto dal demone. Salva due
// file nella cartella da cui è stato lanciato il client, con radice comune
// «mterm-<sessione>-w<id>-<orario>»: un «.txt» convertito in testo leggibile e
// un «.raw» con i byte grezzi (colori e sequenze di escape intatti: un «cat» lo
// ri-mostra identico). L'esito è mostrato come notifica nella barra di stato.
func (c *client) saveBuffer(raw []byte) {
	c.wlMu.Lock()
	id := c.curID
	c.wlMu.Unlock()

	base := fmt.Sprintf("mterm-%s-w%d-%s", sanitizeName(c.name), id, time.Now().Format("150405"))
	txt := base + ".txt"
	rawName := base + ".raw"
	text := plainText(raw)

	var errs []string
	if err := os.WriteFile(txt, text, 0644); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", txt, err))
	}
	if err := os.WriteFile(rawName, raw, 0644); err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", rawName, err))
	}
	if len(errs) > 0 {
		c.setFlash("✗ salvataggio fallito: " + strings.Join(errs, "; "))
	} else {
		c.setFlash(fmt.Sprintf("✔ salvato: %s.{txt,raw} (%d/%d B)", base, len(text), len(raw)))
	}
	c.drawBar()
}

// sanitizeName rende un nome di sessione utilizzabile in un nome di file.
func sanitizeName(s string) string {
	r := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' || r < 0x20 {
			return '-'
		}
		return r
	}, s)
	if r == "" {
		return "sessione"
	}
	return r
}

// plainText converte il buffer grezzo del terminale in testo leggibile: toglie
// le sequenze di escape (CSI, OSC, DCS, designazione set di caratteri, ESC di un
// solo byte) e applica un minimo di logica di riga — ritorno carrello che
// sovrascrive dall'inizio della riga, backspace, tabulazione — così prompt,
// barre di avanzamento e riscritture appaiono come testo pulito invece che
// accavallate. Non è un emulatore di schermo: i riposizionamenti verticali del
// cursore (tipici delle TUI a tutto schermo) non vengono ricostruiti.
func plainText(raw []byte) []byte {
	rs := []rune(string(raw))
	var out []byte
	line := make([]rune, 0, 128)
	col := 0
	put := func(r rune) {
		for len(line) <= col {
			line = append(line, ' ')
		}
		line[col] = r
		col++
	}
	flushLine := func() {
		out = append(out, []byte(strings.TrimRight(string(line), " "))...)
		out = append(out, '\n')
		line = line[:0]
		col = 0
	}
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; {
		case r == 0x1b: // ESC: salta l'intera sequenza
			i += escLen(rs, i) - 1
		case r == '\n':
			flushLine()
		case r == '\r':
			col = 0
		case r == '\b':
			if col > 0 {
				col--
			}
		case r == '\t':
			next := (col/8 + 1) * 8
			for col < next {
				put(' ')
			}
		case r < 0x20 || r == 0x7f: // altri controlli (incluso BEL): ignora
		default:
			put(r)
		}
	}
	if len(line) > 0 {
		flushLine()
	}
	return out
}

// escLen restituisce quanti rune occupa la sequenza di escape che inizia in i
// (rs[i] == ESC), così plainText può saltarla per intero.
func escLen(rs []rune, i int) int {
	n := len(rs)
	if i+1 >= n {
		return 1
	}
	switch rs[i+1] {
	case '[': // CSI: parametri/intermedi (0x20..0x3f) poi un byte finale
		j := i + 2
		for j < n && rs[j] >= 0x20 && rs[j] <= 0x3f {
			j++
		}
		if j < n {
			j++ // byte finale
		}
		return j - i
	case ']', 'P', 'X', '^', '_': // OSC/DCS/SOS/PM/APC: fino a BEL o ST (ESC \)
		j := i + 2
		for j < n {
			if rs[j] == 0x07 {
				j++
				break
			}
			if rs[j] == 0x1b && j+1 < n && rs[j+1] == '\\' {
				j += 2
				break
			}
			j++
		}
		return j - i
	case '(', ')', '*', '+': // designazione set di caratteri: ESC ( X
		if i+2 < n {
			return 3
		}
		return 2
	default: // ESC + un solo byte
		return 2
	}
}

func (c *client) setFlash(msg string) {
	c.flashMu.Lock()
	c.flashMsg = msg
	c.flashUntil = time.Now().Add(4 * time.Second)
	c.flashMu.Unlock()
}

func (c *client) flash() string {
	c.flashMu.Lock()
	defer c.flashMu.Unlock()
	if c.flashMsg != "" && time.Now().Before(c.flashUntil) {
		return c.flashMsg
	}
	return ""
}

// --- utilità ---

func truncRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func shortUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}

func shortHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "host"
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

func dialMode(path string, mode byte) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte{mode}); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
