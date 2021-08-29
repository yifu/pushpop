package main

import (
	"fmt"
	"context"
	"log"
	"time"
	"net"
	"io"
	"os"
	"github.com/grandcat/zeroconf"
)

func main() {
	fmt.Println("Hello world pop.")
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
	}

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
			//io.Copy(os.Stdout, conn)
			fn := entry.Instance
			fmt.Println("Try opening ", fn)
			f, err := os.Create(fn)
			if err != nil {
				log.Fatal(err)
			}

			io.Copy(f, conn)
			return
		}
		log.Println("No more entries.")
	}(entries)

	err = resolver.Browse(context.Background(), "_pushpop._tcp", "local.", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	<-context.Background().Done()
	// Wait some additional time to see debug messages on go routine shutdown.
	time.Sleep(1 * time.Second)
}