package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/discovery"
)

func main() {
	// Gestion du logging conditionnel
	if os.Getenv("DEBUG") != "" {
		f, err := tea.LogToFile("debug.log", "debug")
		if err == nil {
			defer f.Close()
		}
	} else {
		log.SetOutput(io.Discard)
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
			log.Printf("Found entry %v, service: %s at %v:%d\n", entry, entry.Instance, entry.AddrIPv4, entry.Port)
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

	finalExists := fileExists(fn)
	partExists := fileExists(partFn)

	var offset int64

	switch {
	case !finalExists && !partExists:
		offset = 0

	case !finalExists && partExists:
		if fi, err := os.Stat(partFn); err == nil {
			offset = fi.Size()
			fmt.Printf("Resuming download from %s (%s)\n", partFn, formatBytes(offset))
		} else {
			log.Fatalln("Cannot stat .part file:", err)
		}

	case finalExists && !partExists:
		if !*force {
			fmt.Printf("File %s already exists. Overwrite? [y/N]: ", fn)
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Aborted by user.")
				os.Exit(0)
			}
		}
		if err := os.Remove(fn); err != nil {
			log.Fatalln("Cannot remove existing file:", err)
		}
		offset = 0

	case finalExists && partExists:
		if *force {
			_ = os.Remove(fn)
			_ = os.Remove(partFn)
			offset = 0
		} else {
			fmt.Printf("Warning: Both %s and %s exist (inconsistent state)\n", fn, partFn)
			fmt.Println("What do you want to do?")
			fmt.Println("  [1] Keep final file, delete .part (assume complete)")
			fmt.Println("  [2] Keep .part, delete final, resume download")
			fmt.Println("  [3] Delete both, restart from scratch")
			fmt.Println("  [4] Abort")
			fmt.Print("Choice [1-4]: ")
			reader := bufio.NewReader(os.Stdin)
			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(choice)

			switch choice {
			case "1":
				if err := os.Remove(partFn); err != nil {
					log.Fatalln("Cannot remove .part:", err)
				}
				fmt.Println("✓ File already complete:", fn)
				os.Exit(0)
			case "2":
				if err := os.Remove(fn); err != nil {
					log.Fatalln("Cannot remove final file:", err)
				}
				if fi, err := os.Stat(partFn); err == nil {
					offset = fi.Size()
					fmt.Printf("Resuming from .part (%s)\n", formatBytes(offset))
				} else {
					log.Fatalln("Cannot stat .part:", err)
				}
			case "3":
				_ = os.Remove(fn)
				_ = os.Remove(partFn)
				offset = 0
				fmt.Println("Restarting from scratch")
			case "4", "":
				fmt.Println("Aborted by user.")
				os.Exit(0)
			default:
				fmt.Println("Invalid choice. Aborting.")
				os.Exit(1)
			}
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
