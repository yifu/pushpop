package main

import (
	"fmt"
	"os/signal"
	"log"
	"os"
	"syscall"
	"net"
	"github.com/grandcat/zeroconf"
	"strconv"
	"io"
	"path/filepath"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("USAGE: push file")
	}

	fn := os.Args[1]
	tryOpenFile(fn)

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
	portn, err := strconv.Atoi(port)
	if err != nil {
		log.Fatal(err)
	}

	go accept(ln, fn)

	basefn := filepath.Base(fn)

	server, err := zeroconf.Register(basefn, "_pushpop._tcp", "local.", portn, nil, nil)
	if err != nil {
		panic(err)
	}
	defer server.Shutdown()
	
	// Clean exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	}
	
	log.Println("Shutting down.")
}

func tryOpenFile(fn string) {
	f, err := os.Open(fn)
	if err != nil {
		log.Fatal("Unable to open file: ", err)
	}
	f.Close()
}

func accept(ln net.Listener, fn string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go processConn(conn, fn)
	}
}

func processConn(conn net.Conn, fn string) {
	defer conn.Close()

	f, err := os.Open(fn)
	if err != nil {
		log.Println("Unable to open file: ", err)
		return
	}
	defer f.Close()

	_, err = io.Copy(conn, f)
	if err != nil {
		log.Println("Unable to copy file: ", err)
		return
	}
}
