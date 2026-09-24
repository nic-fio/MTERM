//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
)

// win è una finestra: una PTY con la sua shell/programma.
type win struct {
	id     int
	master *os.File
	cmd    *exec.Cmd
	title  string
	alt    bool         // il programma è sullo schermo alternato (vim, less, TUI...)
	buf    []byte       // ultimi byte di output, riversati al reattach / cambio finestra
	modes  map[int]bool // stato dei modi DEC privati tracciati (vedi trackedModes)
	tail   []byte       // coda dell'ultimo output, per sequenze spezzate tra letture
}

// Dimensioni del buffer di replay per finestra: al reattach (o al cambio
// finestra) il demone riversa questi byte al client, così lo schermo torna
// com'era senza dipendere dal fatto che il programma si ridisegni su SIGWINCH.
const (
	bufMax  = 5 << 20 // oltre questa soglia si taglia la parte più vecchia
	bufKeep = bufMax / 2
)

// record accoda output al buffer di replay (chiamare con s.mu tenuto).
// Quando il buffer supera bufMax si scarta la metà più vecchia, riprendendo
// dal primo ESC per non lasciare una sequenza di escape troncata a metà.
func (w *win) record(data []byte) {
	w.buf = append(w.buf, data...)
	if len(w.buf) <= bufMax {
		return
	}
	cut := len(w.buf) - bufKeep
	for i := cut; i < len(w.buf); i++ {
		if w.buf[i] == 0x1b {
			cut = i
			break
		}
	}
	w.buf = append(w.buf[:0], w.buf[cut:]...)
}

// Modi DEC privati tracciati per finestra, con lo stato di default di un
// terminale appena aperto. Sono quelli che, lasciati «sporchi» da una finestra,
// rovinano le altre: su tutti il mouse tracking, che trasforma ogni movimento
// del mouse in byte di input che la shell mostra come caratteri a caso.
var trackedModes = map[int]bool{
	25:   true,  // cursore visibile
	9:    false, // mouse X10
	1000: false, // mouse: click
	1001: false, // mouse: highlight
	1002: false, // mouse: click e trascinamento
	1003: false, // mouse: ogni movimento
	1004: false, // eventi di focus
	1005: false, // codifica mouse UTF-8
	1006: false, // codifica mouse SGR
	1015: false, // codifica mouse urxvt
	1016: false, // codifica mouse SGR-pixel
	2004: false, // bracketed paste
}

// trackedOrder fissa l'ordine di emissione (le codifiche dopo i tracking).
var trackedOrder = []int{25, 9, 1000, 1001, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 2004}

// Quanta coda dell'output precedente rileggere alla scansione successiva:
// basta a coprire una sequenza «ESC [ ? n;n;n h» spezzata tra due letture.
const tailMax = 32

// scanModes aggiorna lo stato dei modi DEC privati (e dello schermo alternato)
// della finestra leggendo le sequenze «ESC [ ? parametri h/l» nell'output
// (chiamare con s.mu tenuto). Una breve coda dell'ultimo blocco viene
// conservata e riscansionata alla chiamata successiva, così una sequenza
// spezzata tra due letture non va persa; rileggere due volte una sequenza già
// applicata è innocuo, perché h/l fissano uno stato, non lo commutano.
func (w *win) scanModes(data []byte) {
	b := data
	if len(w.tail) > 0 {
		b = append(append([]byte(nil), w.tail...), data...)
	}
	for i := 0; i+2 < len(b); i++ {
		if b[i] != 0x1b || b[i+1] != '[' || b[i+2] != '?' {
			continue
		}
		j := i + 3
		for j < len(b) && (b[j] == ';' || (b[j] >= '0' && b[j] <= '9')) {
			j++
		}
		if j >= len(b) {
			break // sequenza incompleta: verrà ripresa dalla coda
		}
		if b[j] != 'h' && b[j] != 'l' {
			continue
		}
		set := b[j] == 'h'
		n := 0
		for _, c := range b[i+3 : j] {
			if c == ';' {
				w.applyMode(n, set)
				n = 0
				continue
			}
			n = n*10 + int(c-'0')
		}
		w.applyMode(n, set)
		i = j
	}
	keep := len(b)
	if keep > tailMax {
		keep = tailMax
	}
	w.tail = append(w.tail[:0], b[len(b)-keep:]...)
}

func (w *win) applyMode(n int, set bool) {
	switch n {
	case 47, 1047, 1049:
		w.alt = set
	default:
		if _, ok := trackedModes[n]; ok {
			w.modes[n] = set
		}
	}
}

// appendModes accoda le sequenze che portano il terminale allo stato dei modi
// privati della finestra (chiamare con s.mu tenuto). Emesse prima del replay:
// spengono ciò che la finestra precedente ha lasciato acceso e riaccendono ciò
// che serve anche se la sequenza originale è uscita dal buffer per taglio.
func (w *win) appendModes(out []byte) []byte {
	hl := func(on bool) byte {
		if on {
			return 'h'
		}
		return 'l'
	}
	out = append(out, fmt.Sprintf("\033[?1049%c", hl(w.alt))...)
	for _, n := range trackedOrder {
		out = append(out, fmt.Sprintf("\033[?%d%c", n, hl(w.modes[n]))...)
	}
	return out
}

// server è il demone: possiede una o più finestre (come screen) e fa da ponte
// verso l'unico client attaccato, mostrandogli la finestra corrente.
type server struct {
	ln   net.Listener
	path string
	logw *os.File // log del demone (eventi, errori, panic), o nil

	mu        sync.Mutex
	wins      []*win
	cur       int
	nextID    int
	cols      int
	rows      int      // righe utili (già ridotte di 1 per la barra dal client)
	client    net.Conn // client attaccato, o nil
	needPaint bool     // ripristino schermo in sospeso per il client appena attaccato

	cmu  sync.Mutex // serializza le scritture (framed) verso il client
	once sync.Once
}

// logf scrive una riga sul log del demone. Il log è l'unico posto dove un
// processo senza terminale può raccontare cosa gli è successo.
func (s *server) logf(format string, args ...any) {
	if s.logw == nil {
		return
	}
	fmt.Fprintf(s.logw, time.Now().Format("2006-01-02 15:04:05")+" "+format+"\n", args...)
}

// runServer è il corpo del demone: non ritorna mai.
func runServer(name string, cmdArgs []string) {
	path := sockPath(name)
	if isAlive(path) {
		os.Exit(1)
	}
	os.Remove(path)

	logw, _ := os.OpenFile(logPath(name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)

	ln, err := net.Listen("unix", path)
	if err != nil {
		if logw != nil {
			fmt.Fprintf(logw, "listen %s: %v\n", path, err)
		}
		os.Exit(1)
	}
	s := &server{ln: ln, path: path, logw: logw, cols: 80, rows: 24}
	s.logf("demone avviato: sessione «%s», pid %d", name, os.Getpid())

	w, err := s.newWindow(cmdArgs, s.nextID)
	if err != nil {
		s.logf("avvio della prima finestra fallito: %v", err)
		ln.Close()
		os.Remove(path)
		os.Exit(1)
	}
	s.nextID++
	s.wins = []*win{w}

	// SIGTERM/SIGHUP: chiusura pulita (i figli vengono terminati, il socket
	// rimosso) invece di lasciare in giro un socket orfano.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, deathSignals...)
	go func() {
		<-sig
		s.logf("ricevuto segnale di terminazione")
		s.shutdown()
	}()

	go s.winReader(w)
	s.acceptLoop()
}

// newWindow apre una PTY e vi lancia la shell (o il comando indicato).
func (s *server) newWindow(args []string, id int) (*win, error) {
	m, slaveName, err := openPTY()
	if err != nil {
		return nil, err
	}
	cmd, err := startShell(slaveName, args)
	if err != nil {
		m.Close()
		return nil, err
	}
	setWinsize(m, s.cols, s.rows)
	modes := make(map[int]bool, len(trackedModes))
	for k, v := range trackedModes {
		modes[k] = v
	}
	return &win{id: id, master: m, cmd: cmd, title: titleFor(args), modes: modes}, nil
}

// startShell lancia la shell (o il comando) collegata allo slave della PTY,
// come leader di sessione con lo slave come terminale di controllo.
func startShell(slaveName string, args []string) (*exec.Cmd, error) {
	// O_NOCTTY: il demone non deve impossessarsi dello slave come proprio
	// terminale di controllo, altrimenti la shell figlia non potrà farlo.
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	defer slave.Close()

	var cmd *exec.Cmd
	if len(args) > 0 {
		cmd = exec.Command(args[0], args[1:]...)
	} else {
		cmd = exec.Command(shellPath())
	}
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.Env = os.Environ() // conserva TERM e il resto: nessuna emulazione
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func shellPath() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

func titleFor(args []string) string {
	if len(args) > 0 {
		return filepath.Base(args[0])
	}
	return filepath.Base(shellPath())
}

// winReader drena l'output di una finestra: sempre nel buffer di replay, e
// verso il client se la finestra è quella corrente. Il drenaggio continuo
// evita che un programma resti bloccato in scrittura mentre si è staccati.
func (s *server) winReader(w *win) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("panic in winReader (finestra %d): %v\n%s", w.id, r, debug.Stack())
			s.closeWindow(w)
		}
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := w.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			isCur := s.cur < len(s.wins) && s.wins[s.cur] == w
			prev := w.alt
			w.scanModes(buf[:n])
			changed := w.alt != prev
			w.record(buf[:n])
			s.mu.Unlock()
			if isCur {
				s.push('D', buf[:n])
				if changed {
					s.sendMeta() // il client deve sapere se riaffermare la regione
				}
			}
		}
		if err != nil {
			s.closeWindow(w) // shell terminata
			return
		}
	}
}

// push invia un frame al client: tipo(1) + lunghezza(uint32 BE) + dati.
func (s *server) push(typ byte, data []byte) {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c == nil {
		return
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(data)))
	s.cmu.Lock()
	_, err := c.Write(hdr[:])
	if err == nil && len(data) > 0 {
		_, err = c.Write(data)
	}
	s.cmu.Unlock()
	if err != nil {
		// Client irraggiungibile: lo si scarta subito, così l'output delle
		// finestre non resta bloccato su una connessione morta.
		s.mu.Lock()
		if s.client == c {
			s.client = nil
		}
		s.mu.Unlock()
		c.Close()
	}
}

// sendMeta invia al client la lista delle finestre e quella corrente.
// Formato: prima riga = id corrente; poi una riga «id\ttitolo» per finestra.
func (s *server) sendMeta() {
	s.mu.Lock()
	var b strings.Builder
	if len(s.wins) > 0 {
		altInt := 0
		if s.wins[s.cur].alt {
			altInt = 1
		}
		// prima riga: id corrente + stato schermo alternato (0/1)
		fmt.Fprintf(&b, "%d\t%d\n", s.wins[s.cur].id, altInt)
		for _, w := range s.wins {
			fmt.Fprintf(&b, "%d\t%s\n", w.id, w.title)
		}
	}
	s.mu.Unlock()
	s.push('M', []byte(b.String()))
}

// activate rende corrente la finestra all'indice idx e la fa ridisegnare.
func (s *server) activate(idx int) {
	s.mu.Lock()
	if idx < 0 || idx >= len(s.wins) {
		s.mu.Unlock()
		return
	}
	s.cur = idx
	w := s.wins[idx]
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	s.repaint(w, cols, rows)
}

// repaint ripristina lo schermo del client: pulisce, riversa il buffer di
// replay della finestra (gli stessi byte che il programma aveva prodotto: lo
// schermo torna com'era anche se il programma non si ridisegna da solo) e
// forza comunque un «jiggle» della winsize (SIGWINCH), utile alle TUI se il
// terminale ha cambiato dimensione nel frattempo.
func (s *server) repaint(w *win, cols, rows int) {
	// Pulisce solo la parte visibile (2J), non lo scrollback (niente 3J): la
	// cronologia del terminale reale resta consultabile.
	s.mu.Lock()
	out := make([]byte, 0, len(w.buf)+128)
	// Prima del replay si riafferma lo stato dei modi privati della finestra:
	// azzera ciò che la finestra precedente ha lasciato acceso (es. il mouse
	// tracking di una TUI, che altrimenti riempirebbe la shell di caratteri
	// spuri a ogni movimento del mouse).
	out = w.appendModes(out)
	out = append(out, "\033[H\033[2J"...)
	out = append(out, w.buf...)
	s.mu.Unlock()
	s.push('D', out)
	s.sendMeta()
	if rows > 1 {
		setWinsize(w.master, cols, rows-1)
	}
	setWinsize(w.master, cols, rows)
}

func (s *server) repaintCurrent() {
	s.mu.Lock()
	if len(s.wins) == 0 {
		s.mu.Unlock()
		return
	}
	w := s.wins[s.cur]
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	s.repaint(w, cols, rows)
}

func (s *server) cycle(delta int) {
	s.mu.Lock()
	n := len(s.wins)
	if n == 0 {
		s.mu.Unlock()
		return
	}
	idx := ((s.cur+delta)%n + n) % n
	s.mu.Unlock()
	s.activate(idx)
}

func (s *server) selectID(id int) {
	s.mu.Lock()
	idx := -1
	for i, w := range s.wins {
		if w.id == id {
			idx = i
			break
		}
	}
	s.mu.Unlock()
	if idx >= 0 {
		s.activate(idx)
	}
}

// createWindow crea una nuova finestra (shell) e vi passa.
func (s *server) createWindow() {
	s.mu.Lock()
	cols, rows := s.cols, s.rows
	id := s.nextID
	s.mu.Unlock()

	w, err := s.newWindow(nil, id)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.nextID++
	s.wins = append(s.wins, w)
	s.cur = len(s.wins) - 1
	s.mu.Unlock()
	go s.winReader(w)
	s.repaint(w, cols, rows)
}

func (s *server) killCurrent() {
	s.mu.Lock()
	if len(s.wins) == 0 {
		s.mu.Unlock()
		return
	}
	w := s.wins[s.cur]
	s.mu.Unlock()
	s.closeWindow(w)
}

// closeWindow rimuove una finestra; se era l'ultima, chiude la sessione.
func (s *server) closeWindow(w *win) {
	s.mu.Lock()
	idx := -1
	for i, x := range s.wins {
		if x == w {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return
	}
	w.master.Close()
	if w.cmd != nil && w.cmd.Process != nil {
		w.cmd.Process.Kill()
	}
	s.wins = append(s.wins[:idx], s.wins[idx+1:]...)
	if len(s.wins) == 0 {
		s.mu.Unlock()
		s.shutdown()
		return
	}
	if s.cur > idx {
		s.cur--
	}
	if s.cur >= len(s.wins) {
		s.cur = len(s.wins) - 1
	}
	if s.cur < 0 {
		s.cur = 0
	}
	cur := s.wins[s.cur]
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	s.repaint(cur, cols, rows)
}

// resize aggiorna la dimensione e la applica a tutte le finestre. Se c'è un
// ripristino in sospeso (client appena attaccato), lo esegue ora che le
// dimensioni reali del suo terminale sono note.
func (s *server) resize(cols, rows int) {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	wins := append([]*win(nil), s.wins...)
	paint := s.needPaint
	s.needPaint = false
	s.mu.Unlock()
	for _, w := range wins {
		setWinsize(w.master, cols, rows)
	}
	if paint {
		s.repaintCurrent()
	}
}

// dumpBuffer invia al client il buffer di replay della finestra corrente in un
// frame 'B', perché il client lo salvi su file (Ctrl-a s). Sono gli stessi byte
// grezzi prodotti dal programma: la conversione in testo leggibile la fa il
// client, che ha il terminale su cui mostrare la conferma.
func (s *server) dumpBuffer() {
	s.mu.Lock()
	var data []byte
	if s.cur < len(s.wins) {
		data = append([]byte(nil), s.wins[s.cur].buf...)
	}
	s.mu.Unlock()
	s.push('B', data)
}

// writeInput invia dati alla shell della finestra corrente.
func (s *server) writeInput(data []byte) {
	s.mu.Lock()
	var m *os.File
	if s.cur < len(s.wins) {
		m = s.wins[s.cur].master
	}
	s.mu.Unlock()
	if m != nil {
		m.Write(data)
	}
}

func (s *server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *server) handleConn(conn net.Conn) {
	var mode [1]byte
	if _, err := io.ReadFull(conn, mode[:]); err != nil {
		conn.Close()
		return
	}
	switch mode[0] {
	case 'P':
		conn.Write([]byte{'o'})
		conn.Close()
	case 'K':
		conn.Write([]byte{'o'})
		conn.Close()
		s.shutdown()
	case 'A':
		s.serveClient(conn)
	default:
		conn.Close()
	}
}

func (s *server) serveClient(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("panic in serveClient: %v\n%s", r, debug.Stack())
			s.mu.Lock()
			if s.client == conn {
				s.client = nil
			}
			s.mu.Unlock()
			conn.Close()
		}
	}()

	s.mu.Lock()
	if s.client != nil {
		old := s.client
		s.client = nil
		old.Close() // sfratta il client precedente
	}
	s.client = conn
	// Il ripristino dello schermo (replay + SIGWINCH) avviene al primo frame
	// 'S' del client, quando le dimensioni del suo terminale sono note: vedi
	// resize(). Il client lo invia subito dopo la connessione.
	s.needPaint = true
	s.mu.Unlock()
	s.logf("client attaccato")

	p := &parser{}
	buf := make([]byte, 8192)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			p.feed(buf[:n], s)
		}
		if err != nil {
			break
		}
	}

	s.mu.Lock()
	if s.client == conn {
		s.client = nil
	}
	s.mu.Unlock()
	conn.Close()
	s.logf("client staccato")
}

func (s *server) shutdown() {
	s.once.Do(func() {
		s.logf("sessione terminata")
		s.mu.Lock()
		wins := append([]*win(nil), s.wins...)
		s.mu.Unlock()
		for _, w := range wins {
			if w.cmd != nil && w.cmd.Process != nil {
				w.cmd.Process.Kill()
			}
		}
		if s.ln != nil {
			s.ln.Close()
		}
		os.Remove(s.path)
		os.Exit(0)
	})
}

// --- parser dei frame client -> server ---
//
//	'S' + rows(u16) + cols(u16)   ridimensiona
//	'D' + len(u16)  + dati        input alla finestra corrente
//	'C'                           crea una nuova finestra
//	'n' / 'p'                     finestra successiva / precedente
//	'g' + id(u16)                 vai alla finestra con quell'id
//	'x'                           chiudi la finestra corrente
//	'b'                           richiedi il dump del buffer (risposta: frame 'B')

const (
	stType = iota
	stSize
	stDlen
	stData
	stGoto
)

type parser struct {
	st   int
	got  int
	hb   [4]byte
	dlen int
}

func (p *parser) feed(b []byte, s *server) {
	i := 0
	for i < len(b) {
		switch p.st {
		case stType:
			t := b[i]
			i++
			switch t {
			case 'S':
				p.st, p.got = stSize, 0
			case 'D':
				p.st, p.got = stDlen, 0
			case 'g':
				p.st, p.got = stGoto, 0
			case 'C':
				s.createWindow()
			case 'n':
				s.cycle(+1)
			case 'p':
				s.cycle(-1)
			case 'x':
				s.killCurrent()
			case 'b':
				s.dumpBuffer()
			}
		case stSize:
			p.hb[p.got] = b[i]
			p.got++
			i++
			if p.got == 4 {
				rows := int(p.hb[0])<<8 | int(p.hb[1])
				cols := int(p.hb[2])<<8 | int(p.hb[3])
				s.resize(cols, rows)
				p.st = stType
			}
		case stGoto:
			p.hb[p.got] = b[i]
			p.got++
			i++
			if p.got == 2 {
				id := int(p.hb[0])<<8 | int(p.hb[1])
				s.selectID(id)
				p.st = stType
			}
		case stDlen:
			p.hb[p.got] = b[i]
			p.got++
			i++
			if p.got == 2 {
				p.dlen = int(p.hb[0])<<8 | int(p.hb[1])
				if p.dlen == 0 {
					p.st = stType
				} else {
					p.st = stData
				}
			}
		case stData:
			take := len(b) - i
			if take > p.dlen {
				take = p.dlen
			}
			s.writeInput(b[i : i+take])
			i += take
			p.dlen -= take
			if p.dlen == 0 {
				p.st = stType
			}
		}
	}
}
