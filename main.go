//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]

	// il sottocomando nascosto __daemon è il processo demone vero e proprio
	if len(args) >= 2 && args[0] == "__daemon" {
		runServer(args[1], args[2:])
		return
	}

	if wantsHelp(args) {
		displayHelp()
		return
	}

	// senza argomenti: apre (o crea) la sessione predefinita «Default».
	if len(args) == 0 {
		name := "Default"
		if isAlive(sockPath(name)) {
			if err := attach(name); err != nil {
				fatal(err)
			}
		} else if err := cmdNew(name, nil); err != nil {
			fatal(err)
		}
		return
	}

	switch args[0] {
	case "ls", "list":
		cmdList()
	case "new", "n":
		name, cmd := parseNewArgs(args[1:])
		if err := cmdNew(name, cmd); err != nil {
			fatal(err)
		}
	case "attach", "a":
		if len(args) < 2 {
			fatal(fmt.Errorf("manca il nome della sessione"))
		}
		if err := attach(mustName(args[1])); err != nil {
			fatal(err)
		}
	case "kill", "k":
		if len(args) < 2 {
			fatal(fmt.Errorf("manca il nome della sessione"))
		}
		if err := cmdKill(mustName(args[1])); err != nil {
			fatal(err)
		}
	default:
		// «mterm NOME»: se esiste ci si attacca, altrimenti la si crea.
		name := mustName(args[0])
		if isAlive(sockPath(name)) {
			if err := attach(name); err != nil {
				fatal(err)
			}
		} else if err := cmdNew(name, nil); err != nil {
			fatal(err)
		}
	}
}

// parseNewArgs interpreta «[nome] [-- comando...]».
func parseNewArgs(a []string) (name string, cmd []string) {
	for i, v := range a {
		if v == "--" {
			cmd = a[i+1:]
			a = a[:i]
			break
		}
	}
	if len(a) > 0 {
		name = a[0]
	}
	return name, cmd
}

// cmdNew crea la sessione (lanciando il demone) e vi si attacca.
func cmdNew(name string, cmd []string) error {
	if name == "" {
		name = pickName()
	} else {
		name = mustName(name)
	}
	path := sockPath(name)
	if isAlive(path) {
		return fmt.Errorf("la sessione «%s» esiste già", name)
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	nul, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer nul.Close()

	d := exec.Command(self, append([]string{"__daemon", name}, cmd...)...)
	d.Stdin, d.Stdout, d.Stderr = nul, nul, nul
	d.Env = os.Environ()
	d.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := d.Start(); err != nil {
		return err
	}
	d.Process.Release()

	for i := 0; i < 200; i++ {
		if isAlive(path) {
			return attach(name)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("avvio della sessione «%s» fallito (dettagli in %s)", name, logPath(name))
}

// cmdKill termina una sessione.
func cmdKill(name string) error {
	path := sockPath(name)
	conn, err := dialMode(path, 'K')
	if err != nil {
		os.Remove(path)
		return fmt.Errorf("nessuna sessione «%s»", name)
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	var b [1]byte
	conn.Read(b[:])
	conn.Close()
	fmt.Printf("sessione «%s» terminata\n", name)
	return nil
}

// cmdList elenca le sessioni vive e ripulisce i socket morti.
func cmdList() {
	dir := sockDir()
	entries, _ := os.ReadDir(dir)
	var live []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sock") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".sock")
		p := filepath.Join(dir, e.Name())
		if isAlive(p) {
			live = append(live, name)
		} else {
			os.Remove(p) // socket orfano
		}
	}
	if len(live) == 0 {
		fmt.Println("nessuna sessione attiva")
		return
	}
	sort.Strings(live)
	fmt.Printf("sessioni attive (%d):\n", len(live))
	for _, n := range live {
		fmt.Printf("  %s\n", n)
	}
}

// --- gestione socket / nomi ---

func sockDir() string {
	var d string
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		d = filepath.Join(base, "mterm")
	} else {
		d = fmt.Sprintf("/tmp/mterm-%d", os.Getuid())
	}
	os.MkdirAll(d, 0700)
	return d
}

func sockPath(name string) string {
	return filepath.Join(sockDir(), name+".sock")
}

// logPath è il file di log del demone della sessione (eventi, errori, panic).
func logPath(name string) string {
	return filepath.Join(sockDir(), name+".log")
}

// isAlive verifica se un demone risponde sul socket, senza staccare eventuali
// client (usa il ping 'P').
func isAlive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := conn.Write([]byte{'P'}); err != nil {
		return false
	}
	var b [1]byte
	if _, err := conn.Read(b[:]); err != nil {
		return false
	}
	return b[0] == 'o'
}

// pickName sceglie il più piccolo intero non ancora usato come nome.
func pickName() string {
	used := map[string]bool{}
	entries, _ := os.ReadDir(sockDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock") {
			used[strings.TrimSuffix(e.Name(), ".sock")] = true
		}
	}
	for i := 0; ; i++ {
		n := strconv.Itoa(i)
		if !used[n] {
			return n
		}
	}
}

// mustName valida un nome di sessione (niente «/» o vuoti).
func mustName(name string) string {
	if name == "" || strings.ContainsAny(name, "/ \t") {
		fatal(fmt.Errorf("nome di sessione non valido: «%s»", name))
	}
	return name
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "mterm: "+err.Error())
	os.Exit(1)
}

// wantsHelp riconosce le richieste d'aiuto esplicite. Senza argomenti NON si
// mostra l'aiuto: si apre la sessione predefinita (vedi main).
func wantsHelp(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		return true
	}
	return false
}
