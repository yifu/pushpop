package main

import (
	"fmt"
	"context"
	"log"
	"net"
	"io"
	"os"
	"github.com/grandcat/zeroconf"
)

func main() {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())

	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			log.Printf("%+v\n", entry)
			ip := entry.AddrIPv4[0]
			port := entry.Port
			ipport := fmt.Sprintf("%v:%v", ip, port)
			conn, err := net.Dial("tcp", ipport)
			if err != nil {
				log.Fatal(err)
			}

			fn := entry.Instance
			fmt.Println("Try opening ", fn)
			f, err := os.Create(fn)
			if err != nil {
				log.Fatal(err)
			}

			io.Copy(f, conn)
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