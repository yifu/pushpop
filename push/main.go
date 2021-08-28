package main

import (
	"fmt"
	"os/signal"
	"log"
	"os"
	"syscall"
	"net"
	"github.com/grandcat/zeroconf"
)

func main() {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal(err)
	}
	addr := ln.Addr()
	hostport := addr.String()
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("host:", host, ", port:", port)

	server, err := zeroconf.Register("GoZeroconf", "_workstation._tcp", "local.", 42424, []string{"txtv=0", "lo=1", "la=2"}, nil)
	if err != nil {
		panic(err)
	}
	defer server.Shutdown()
	
	// Clean exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		// Exit by user
	// case <-time.After(time.Second * 120):
	// 	// Exit by timeout
	}
	
	log.Println("Shutting down.")
}