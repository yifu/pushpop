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

// --- BLAKE3 hash cache and synchronization ---
type hashResult struct {
	hash string
	err  error
}

var (
	hashCache = make(map[string]hashResult)
	hashMu    sync.Mutex
	hashCond  = make(map[string]*sync.Cond) // one condition per file
)

// getBlake3 computes or retrieves the BLAKE3 hash of a file with synchronization and caching.
func getBlake3(path string) (string, error) {
	hashMu.Lock()
	if res, ok := hashCache[path]; ok {
		hashMu.Unlock()
		return res.hash, res.err
	}
	if c, ok := hashCond[path]; ok {
		c.Wait()
		res := hashCache[path]
		hashMu.Unlock()
		return res.hash, res.err
	}
	c := sync.NewCond(&hashMu)
	hashCond[path] = c
	hashMu.Unlock()

	h, err := computeBlake3(path)

	hashMu.Lock()
	delete(hashCond, path)
	hashCache[path] = hashResult{h, err}
	c.Broadcast()
	hashMu.Unlock()
	return h, err
}

// computeBlake3 computes the BLAKE3 hash of a file.
func computeBlake3(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := blake3.New()
	buf := make([]byte, 128*1024) // 128 KiB buffer
	for {
		n, err := file.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// Option A: simple HTTP server that serves the file's directory.
// The server listens on ":0" to get a system-assigned available port,
// then registers the mDNS service with that port.
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
		user := r.Header.Get("X-PushPop-User")
		if user == "" {
			user = "(unknown)"
		}
		ip := r.RemoteAddr
		if idx := strings.LastIndex(ip, ":"); idx != -1 {
			ip = ip[:idx]
		}

		log.Printf("[%s] %s is requesting BLAKE3 hash", ip, user)

		hash, err := getBlake3(fn)
		if err != nil {
			http.Error(w, "Failed to compute hash", http.StatusInternalServerError)
			log.Printf("[%s] %s failed to get BLAKE3 hash: %v", ip, user, err)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, hash)

		log.Printf("BLAKE3 hash: '%v'", hash)

		log.Printf("[%s] %s finished requesting BLAKE3 hash", ip, user)
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
