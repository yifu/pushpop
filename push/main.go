package main

import (
	"fmt"
	"os/signal"
	"log"
	"os"
	"syscall"
	"github.com/grandcat/zeroconf"
)

func main() {
	fmt.Println("Hello world.")
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