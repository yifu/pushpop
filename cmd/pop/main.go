package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/discovery"
)

func main() {
	// Parse flags: --force
	force := flag.Bool("force", false, "overwrite existing file without confirmation")
	flag.Parse()

	var username string
	args := flag.Args()
	if len(args) == 0 {
		username = os.Getenv("USER")
		if username == "" {
			log.Fatal("unable to determine username")
		}
	} else if len(args) == 1 {
		username = args[0]
	} else {
		fmt.Println("USAGE: pop [--force] <username>")
		os.Exit(1)
	}

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())

	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			log.Printf("%+v\n", entry)

			entryUsername, err := discovery.GetUserName(entry)
			if err != nil {
				log.Println(err)
				continue
			}

			if username != entryUsername {
				continue
			}

			ip, err := discovery.FindMatchingIP(entry.AddrIPv4)
			if err != nil {
				log.Println(err)
				continue
			}
			port := entry.Port
			url := fmt.Sprintf("http://%v:%v/", ip, port)

			fn := entry.Instance
			partFn := fn + ".part"

			// Check if final file exists
			if fileExists(fn) && !*force {
				fmt.Printf("File %s already exists. Overwrite? [y/N]: ", fn)
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Println("Aborted by user.")
					continue
				}
			}

			// Check if .part file exists for resume
			var offset int64 = 0
			if fileExists(partFn) {
				fi, err := os.Stat(partFn)
				if err == nil {
					offset = fi.Size()
				}
			}

			// Prepare HTTP request (with Range if resuming)
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				log.Println("http request error:", err)
				continue
			}
			if offset > 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			}
			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				log.Println("http get error:", err)
				continue
			}
			defer resp.Body.Close()

			// Handle server response
			if offset > 0 && resp.StatusCode == 206 {
				fmt.Printf("Resuming download at byte %d...\n", offset)
				f, err := os.OpenFile(partFn, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					log.Println("Unable to open .part file for append:", err)
					continue
				}
				_, err = io.Copy(f, resp.Body)
				f.Close()
				if err != nil {
					log.Println("copy error:", err)
					continue
				}
			} else if offset > 0 && resp.StatusCode == 200 {
				fmt.Println("Warning: server does not support HTTP Range. Restarting download from the beginning.")
				f, err := os.Create(partFn)
				if err != nil {
					log.Println(err)
					continue
				}
				_, err = io.Copy(f, resp.Body)
				f.Close()
				if err != nil {
					log.Println("copy error:", err)
					continue
				}
			} else {
				// Fresh download
				f, err := os.Create(partFn)
				if err != nil {
					log.Println(err)
					continue
				}
				_, err = io.Copy(f, resp.Body)
				f.Close()
				if err != nil {
					log.Println("copy error:", err)
					continue
				}
			}

			// Rename .part to final name
			err = os.Rename(partFn, fn)
			if err != nil {
				log.Println("rename error:", err)
				continue
			}
			fmt.Printf("Download complete: %s\n", fn)
			cancel()
			return
		}
		log.Println("No more entries.")
	}(entries)

	err = resolver.Browse(ctx, "_pushpop._tcp", "local.", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	<-ctx.Done()
}

// fileExists returns true if the file exists and is not a directory.
func fileExists(name string) bool {
	fi, err := os.Stat(name)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}
