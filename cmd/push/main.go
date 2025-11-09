package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	"github.com/grandcat/zeroconf"
)

// Option A: simple HTTP server that serves the file's directory.
// The server listens on ":0" to get a system-assigned available port,
// then registers the mDNS service with that port.
func main() {
	if len(os.Args) != 2 {
		log.Fatal("USAGE: push file")
	}

	fn := os.Args[1]
	tryOpenFile(fn)

	dir := filepath.Dir(fn)
	base := filepath.Base(fn)

	// Create a TCP listener on a dynamic port (":0")
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal(err)
	}

	// Retrieve the chosen port
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		log.Fatal(err)
	}
	fmt.Printf("Serving %s on port %s\n", base, portStr)

	// Prepare the mDNS announcement using the file name and current user
	usr, err := user.Current()
	if err != nil {
		ln.Close()
		log.Fatal(err)
	}
	kv := fmt.Sprintf("user=%s", usr.Username)
	text := []string{kv}

	portn := 0
	fmt.Sscanf(portStr, "%d", &portn)

	server, err := zeroconf.Register(base, "_pushpop._tcp", "local.", portn, text, nil)
	if err != nil {
		ln.Close()
		log.Fatal(err)
	}
	defer server.Shutdown()

	// HTTP server that serves the directory containing the file.
	mux := http.NewServeMux()
	// Serve the file at the root: GET / -> file contents
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If root is requested, serve the file directly
		if r.URL.Path == "/" {
			http.ServeFile(w, r, fn)
			return
		}
		// Otherwise, serve files from the directory
		http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
	})

	srv := &http.Server{Handler: mux}

	// Start the HTTP server in a goroutine
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Print a user-friendly URL (use 0.0.0.0 if necessary)
	fmt.Printf("URL: http://<host>:%s/%s\n", portStr, base)

	// Clean exit: wait for SIGINT/SIGTERM then shutdown gracefully
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("Shutting down.")
}

func tryOpenFile(fn string) {
	f, err := os.Open(fn)
	if err != nil {
		log.Fatal("Unable to open file: ", err)
	}
	f.Close()
}
