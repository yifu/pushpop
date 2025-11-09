package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/discovery"
)

func main() {
	var username string
	if len(os.Args) == 1 {
		username = os.Getenv("USER")
		if username == "" {
			log.Fatal("unable to determine username")
		}
	} else if len(os.Args) == 2 {
		username = os.Args[1]
	} else {
		fmt.Println("USAGE: pop <username>")
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
			// Download the file over HTTP from the push server.
			url := fmt.Sprintf("http://%v:%v/", ip, port)
			resp, err := http.Get(url)
			if err != nil {
				log.Println("http get error:", err)
				continue
			}
			if resp.StatusCode != 200 {
				log.Printf("unexpected http status: %s", resp.Status)
				resp.Body.Close()
				continue
			}

			fn := entry.Instance
			fmt.Println("Try opening ", fn)
			f, err := os.Create(fn)
			if err != nil {
				log.Println(err)
				resp.Body.Close()
				continue
			}

			_, err = io.Copy(f, resp.Body)
			resp.Body.Close()
			f.Close()
			if err != nil {
				log.Println("copy error:", err)
				continue
			}
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
