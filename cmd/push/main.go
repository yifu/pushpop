package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/gosuri/uiprogress"
	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/transfer"
)

func main() {
	uiprogress.Start()
	defer uiprogress.Stop()

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

	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	kv := fmt.Sprintf("user=%s", usr.Username)
	text := []string{kv}

	go transfer.Accept(ln, fn)

	basefn := filepath.Base(fn)

	server, err := zeroconf.Register(basefn, "_pushpop._tcp", "local.", portn, text, nil)
	if err != nil {
		panic(err)
	}
	defer server.Shutdown()

	// Clean exit.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down.")
}

func tryOpenFile(fn string) {
	f, err := os.Open(fn)
	if err != nil {
		log.Fatal("Unable to open file: ", err)
	}
	f.Close()
}
