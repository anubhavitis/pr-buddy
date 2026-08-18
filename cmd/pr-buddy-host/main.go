package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
	"github.com/anubhavitis/pr-buddy/internal/host"
)

func main() {
	augmentPATH()
	addr := flag.String("addr", "127.0.0.1:17342", "listen address (loopback only)")
	flag.Parse()

	hostName, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatal(err)
	}
	if ip := net.ParseIP(hostName); ip == nil || !ip.IsLoopback() {
		if hostName != "localhost" {
			log.Fatalf("refusing to listen on %s: loopback only", *addr)
		}
	}

	s := &http.Server{
		Addr:    *addr,
		Handler: host.Handler(&host.Completer{Exec: xexec.Real{}}),
	}
	log.Printf("pr-buddy-host listening on http://%s", *addr)
	if err := s.ListenAndServe(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

// Chrome-launched or launchd processes do not load the user's shell rc, so
// claude/grok installed under ~/.local or ~/.grok would otherwise be invisible.
func augmentPATH() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	extras := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".grok", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	os.Setenv("PATH", strings.Join(append(extras, os.Getenv("PATH")), string(os.PathListSeparator)))
}
