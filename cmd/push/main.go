package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grandcat/zeroconf"
	"github.com/zeebo/blake3"
)

// État de hash par fichier (prépare multi-fichiers)
type hashState struct {
	hash string
	err  error
	done bool
}

var (
	hashMu    sync.RWMutex
	hashStore = map[string]*hashState{}
)

func ensureHashAsync(path string) {
	hashMu.Lock()
	if _, ok := hashStore[path]; ok {
		hashMu.Unlock()
		return
	}
	st := &hashState{}
	hashStore[path] = st
	hashMu.Unlock()

	go func() {
		h, err := computeBlake3(path)
		hashMu.Lock()
		st.hash = h
		st.err = err
		st.done = true
		hashMu.Unlock()
	}()
}

func getHash(path string) (st *hashState) {
	hashMu.RLock()
	st = hashStore[path]
	hashMu.RUnlock()
	return
}

func computeBlake3(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := blake3.New()
	buf := make([]byte, 256*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func main() {
	// Initialisation du logging selon DEBUG
	if os.Getenv("DEBUG") == "" {
		log.SetOutput(io.Discard)
	} else {
		f, err := os.Create("push_debug.log")
		if err == nil {
			log.SetOutput(f)
			defer f.Close()
		}
	}

	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "USAGE: push file")
		os.Exit(1)
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
	text := []string{"user=" + usr.Username}

	var portn int
	fmt.Sscanf(portStr, "%d", &portn)

	server, err := zeroconf.Register(base, "_pushpop._tcp", "local.", portn, text, nil)
	if err != nil {
		ln.Close()
		log.Fatal(err)
	}
	defer server.Shutdown()

	ensureHashAsync(fn)

	mux := http.NewServeMux()

	// Serve the file at the root: GET / -> file contents
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Extract client information
		clientIP := r.RemoteAddr
		// Try to get the real IP if behind a proxy
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			clientIP = forwarded
		}
		// Extract username from custom header
		username := r.Header.Get("X-PushPop-User")
		if username == "" {
			username = "unknown"
		}

		// Log download start
		fmt.Printf("📥 Download started by: %s from %s\n", username, clientIP)

		// If root is requested, serve the file directly
		if r.URL.Path == "/" {
			http.ServeFile(w, r, fn)
			fmt.Printf("✅ Download completed by: %s from %s\n", username, clientIP)
			return
		}
		// Otherwise, serve files from the directory
		http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
	})

	log.Println("New route for BLAKE3 hash is ", "/"+fn+".blake3")
	// Serve the BLAKE3 hash file
	mux.HandleFunc("/"+base+".blake3", func(w http.ResponseWriter, r *http.Request) {
		// Extract user and IP (same logic as main file download)
		userH := r.Header.Get("X-PushPop-User")
		if userH == "" {
			userH = "(unknown)"
		}
		ip := r.RemoteAddr
		if idx := strings.LastIndex(ip, ":"); idx != -1 {
			ip = ip[:idx]
		}
		st := getHash(fn)
		if st == nil || !st.done {
			http.Error(w, "BLAKE3 pending", http.StatusServiceUnavailable)
			log.Printf("[%s] %s requested BLAKE3 (pending)", ip, userH)
			return
		}
		if st.err != nil {
			http.Error(w, "BLAKE3 error", http.StatusInternalServerError)
			log.Printf("[%s] %s requested BLAKE3 (error: %v)", ip, userH, st.err)
			return
		}
		log.Printf("[%s] %s served BLAKE3 %s", ip, userH, st.hash)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, st.hash+"\n")
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
