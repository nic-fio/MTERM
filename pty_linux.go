//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ioctl per gestire la pseudo-terminale (valori linux/amd64).
const (
	tiocsptlck = 0x40045431 // sblocca lo slave
	tiocgptn   = 0x80045430 // numero dello slave
)

// openPTY apre una nuova coppia master/slave e restituisce il file master
// aperto e il percorso dello slave (/dev/pts/N).
func openPTY() (*os.File, string, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	var unlock int32 // 0 = sblocca
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(),
		tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, "", e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(),
		tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, "", e
	}
	return m, fmt.Sprintf("/dev/pts/%d", n), nil
}

// setWinsize imposta la dimensione della finestra sulla pty aperta come f.
// Impostarla sul master fa arrivare SIGWINCH al processo dentro la pty, che
// così si ridisegna.
func setWinsize(f *os.File, cols, rows int) {
	ws := &winsize{Col: uint16(cols), Row: uint16(rows)}
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(ws)))
}
