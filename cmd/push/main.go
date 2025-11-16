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

	"sync"

	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/blake"
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

	h, err := blake.CalcBlake3(path)

	hashMu.Lock()
	delete(hashCond, path)
	hashCache[path] = hashResult{h, err}
	c.Broadcast()
	hashMu.Unlock()
	return h, err
}

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

	// Serve the BLAKE3 hash at /<filename>.blake3
	mux.HandleFunc("/"+base+".blake3", func(w http.ResponseWriter, r *http.Request) {
		// Extract client information
		clientIP := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			clientIP = forwarded
		}
		username := r.Header.Get("X-PushPop-User")
		if username == "" {
			username = "unknown"
		}

		fmt.Printf("🔐 BLAKE3 hash requested by: %s from %s\n", username, clientIP)

		hash, err := getBlake3(fn)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "error: %v\n", err)
			return
		}
		fmt.Fprintln(w, hash)
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
