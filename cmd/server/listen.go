package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// listenFdsStart ist der erste Dateideskriptor, den systemd bei
// Socket-Aktivierung übergibt (SD_LISTEN_FDS_START).
const listenFdsStart = 3

// listen liefert den Socket, auf dem der Server Anfragen annimmt.
//
// Unter NexoraPanel legt systemd den Socket an (Unix-Socket, nur für nginx
// erreichbar) und reicht ihn per Socket-Aktivierung weiter. So kann kein
// anderer Benutzer des Servers die Adresse vorher belegen oder die App unter
// Umgehung von nginx direkt ansprechen. Ohne systemd-Socket wird wie bisher
// auf addr (TCP) gelauscht.
func listen(addr string) (net.Listener, string, error) {
	if ln, ok, err := systemdListener(); ok || err != nil {
		if err != nil {
			return nil, "", err
		}
		return ln, "systemd:" + ln.Addr().String(), nil
	}
	ln, err := net.Listen("tcp", addr)
	return ln, addr, err
}

func systemdListener() (net.Listener, bool, error) {
	pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if pid != os.Getpid() {
		return nil, false, nil
	}
	n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	// Nicht an Kindprozesse (ffmpeg & Co.) weiterreichen.
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
	if n != 1 {
		return nil, true, fmt.Errorf("systemd übergibt %d Sockets, erwartet 1", n)
	}
	f := os.NewFile(uintptr(listenFdsStart), "systemd-socket")
	ln, err := net.FileListener(f)
	f.Close() // FileListener hat den Deskriptor dupliziert
	if err != nil {
		return nil, true, fmt.Errorf("systemd-Socket: %w", err)
	}
	return ln, true, nil
}
