package main

import (
	"fmt"
	"context"
	"log"
	"net"
	"io"
	"os"
	"github.com/grandcat/zeroconf"
	"os/user"
	"regexp"
)

func main() {
	var username string
	if len(os.Args) == 1 {
		usr, err := user.Current()
		if err != nil {
			log.Fatal(err)
		}
		username = usr.Username
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

			entry_username, err := getUserName(entry)
			if err != nil {
				log.Println(err)
				continue
			}

			if username != entry_username {
				continue
			}

			ip, err := findMatchingIP(entry.AddrIPv4)
			if err != nil {
				log.Fatal(err)
			}
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

func getUserName(entry *zeroconf.ServiceEntry) (string, error) {
	var reg = regexp.MustCompile("(\\w+)=(\\w+)")
	for _, val := range entry.Text {
		//fmt.Printf("val = %q\n", val)
		data := reg.FindAllStringSubmatch(val, -1)
		//fmt.Printf("data = %q\n", data)
		if len(data) < 1 || len(data[0]) != 3 {
			continue
		}
		if data[0][1] == "user" {
			return data[0][2], nil
		}
	}
	return "", fmt.Errorf("User key/value pair not found")
}

func findMatchingIP(ips []net.IP) (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Fatal(err);
	}
	for _, iface := range ifaces {
		//fmt.Println("iface name: ", iface.Name)
		iface_addrs, err := iface.Addrs()
		if err != nil {
			log.Fatal(err)
		}
		//fmt.Println(addrs)
		for _, iface_addr := range iface_addrs {
			_, iface_net, err := net.ParseCIDR(iface_addr.String())
			if err != nil {
				log.Fatal(err)
			}
			for _, ip := range ips {
				if iface_net.Contains(ip) {
					fmt.Println("Found an interface: ", iface.Name,
								" with ip: ", iface_addr,
								" with net: ", iface_net,
								" corresponding to ip: ", ip)
					return ip.String(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("Found no matching interface")
}