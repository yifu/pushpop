package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/discovery"
)

func main() {
	if len(os.Getenv("DEBUG")) > 0 {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			fmt.Println("fatal:", err)
			os.Exit(1)
		}
		defer f.Close()
	}

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
	defer cancel()

	type serviceInfo struct {
		url      string
		filename string
	}
	foundService := make(chan serviceInfo, 1)

	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			entryUsername, err := discovery.GetUserName(entry)
			if err != nil || entryUsername != username {
				continue
			}
			ip, err := discovery.FindMatchingIP(entry.AddrIPv4)
			if err != nil {
				continue
			}
			foundService <- serviceInfo{
				url:      fmt.Sprintf("http://%v:%v/", ip, entry.Port),
				filename: entry.Instance,
			}
			return
		}
		close(foundService)
	}(entries)

	if err = resolver.Browse(ctx, "_pushpop._tcp", "local.", entries); err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	service, ok := <-foundService
	if !ok {
		log.Fatalln("No service found for user:", username)
	}

	fn := service.filename
	partFn := fn + ".part"

	if fileExists(fn) && !*force {
		fmt.Printf("File %s already exists. Overwrite? [y/N]: ", fn)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted by user.")
			os.Exit(0)
		}
	}

	var offset int64
	if fileExists(partFn) {
		if fi, err := os.Stat(partFn); err == nil {
			offset = fi.Size()
		}
	}

	model := newDownloadModel(username, fn, partFn, service.url, offset)
	p := tea.NewProgram(model, tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		log.Fatalln("UI error:", err)
	}

	dm := finalModel.(downloadModel)
	if dm.err != nil {
		log.Fatalln("Error:", dm.err)
	}
	fmt.Println("✓ Download complete and verified:", fn)
}
