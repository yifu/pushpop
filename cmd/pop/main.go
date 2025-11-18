package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grandcat/zeroconf"
	"github.com/yifu/pushpop/pkg/blake"
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

	// Parse flags: --force
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
			url := fmt.Sprintf("http://%v:%v/", ip, port)

			fn := entry.Instance
			partFn := fn + ".part"

			// Check if final file exists
			if fileExists(fn) && !*force {
				fmt.Printf("File %s already exists. Overwrite? [y/N]: ", fn)
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Println("Aborted by user.")
					continue
				}
			}

			// Check if .part file exists for resume
			var offset int64 = 0
			if fileExists(partFn) {
				fi, err := os.Stat(partFn)
				if err == nil {
					offset = fi.Size()
				}
			}

			// Prepare HTTP request (with Range if resuming)
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				log.Println("http request error:", err)
				continue
			}
			if offset > 0 {
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			}
			// Send username in custom header for server-side logging
			req.Header.Set("X-PushPop-User", username)
			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				log.Println("http get error:", err)
				continue
			}
			defer resp.Body.Close()

			// Get total size for progress bar
			totalSize := resp.ContentLength
			if offset > 0 && resp.StatusCode == 206 {
				totalSize += offset // Add the already downloaded part
			}

			// Create Bubble Tea model for progress
			prog := progress.New(progress.WithDefaultGradient())
			prog.Width = 50

			model := downloadModel{
				filename:            fn,
				progress:            prog,
				totalBytes:          totalSize,
				downloadedBytes:     offset,
				lastUpdate:          time.Now(),
				lastDownloadedBytes: offset,
			}

			p := tea.NewProgram(model)

			// Handle server response
			var f *os.File
			if offset > 0 && resp.StatusCode == 206 {
				fmt.Printf("Resuming download at byte %d...\n", offset)
				f, err = os.OpenFile(partFn, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					log.Println("Unable to open .part file for append:", err)
					continue
				}
			} else if offset > 0 && resp.StatusCode == 200 {
				fmt.Println("Warning: server does not support HTTP Range. Restarting download from the beginning.")
				f, err = os.Create(partFn)
				if err != nil {
					log.Println(err)
					continue
				}
				offset = 0
				model.downloadedBytes = 0
			} else {
				// Fresh download
				f, err = os.Create(partFn)
				if err != nil {
					log.Println(err)
					continue
				}
			}

			// Start Bubble Tea in background
			go func() {
				if _, err := p.Run(); err != nil {
					log.Println("Error running progress UI:", err)
				}
			}()

			// Create progress writer wrapper
			pw := &progressWriter{
				written: offset,
				program: p,
			}

			// Copy with progress tracking
			multiWriter := io.MultiWriter(f, pw)
			_, err = io.Copy(multiWriter, resp.Body)
			f.Close()

			// Signal completion
			p.Send(doneMsg{err: err})
			time.Sleep(100 * time.Millisecond) // Give UI time to update

			if err != nil {
				log.Println("copy error:", err)
				continue
			}

			// Rename .part to final name
			err = os.Rename(partFn, fn)
			if err != nil {
				log.Println("rename error:", err)
				continue
			}
			fmt.Printf("Download complete: %s\n", fn)

			// BLAKE3 integrity verification
			blake3URL := url + fn + ".blake3"
			respHash, err := http.Get(blake3URL)
			if err != nil {
				log.Printf("Unable to retrieve BLAKE3 hash: %v", err)
				continue
			}
			defer respHash.Body.Close()
			remoteHashBytes, err := io.ReadAll(respHash.Body)
			if err != nil {
				log.Printf("Error reading remote hash: %v", err)
				continue
			}
			remoteHash := strings.TrimSpace(string(remoteHashBytes))

			// Compute local hash
			localHash, err := blake.CalcBlake3(fn)
			if err != nil {
				log.Printf("Error computing local hash: %v", err)
				continue
			}
			if localHash != remoteHash {
				log.Printf("ERROR: file integrity check failed (BLAKE3 mismatch)\nexpected: %s\nobtained: %s", remoteHash, localHash)
				// Delete corrupted file
				err := os.Remove(fn)
				if err != nil {
					log.Printf("Unable to delete corrupted file: %v", err)
				}
				continue
			}
			fmt.Println("BLAKE3 integrity check OK.")

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

// fileExists returns true if the file exists and is not a directory.
func fileExists(name string) bool {
	fi, err := os.Stat(name)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

// Bubble Tea model for download progress
type downloadModel struct {
	filename            string
	progress            progress.Model
	totalBytes          int64
	downloadedBytes     int64
	err                 error
	done                bool
	speed               float64 // bytes per second
	lastUpdate          time.Time
	lastDownloadedBytes int64
}

// Messages
type progressMsg struct {
	bytes int64
}

type doneMsg struct {
	err error
}

type speedTickMsg time.Time

func (m downloadModel) Init() tea.Cmd {
	return tickSpeed()
}

func tickSpeed() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return speedTickMsg(t)
	})
}

func (m downloadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return m, tea.Quit
		}

	case progressMsg:
		m.downloadedBytes = msg.bytes
		if m.totalBytes > 0 {
			percent := float64(m.downloadedBytes) / float64(m.totalBytes)
			return m, m.progress.SetPercent(percent)
		}
		return m, nil

	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd

	case speedTickMsg:
		now := time.Time(msg)
		if !m.lastUpdate.IsZero() {
			elapsed := now.Sub(m.lastUpdate).Seconds()
			if elapsed > 0 {
				bytesDiff := m.downloadedBytes - m.lastDownloadedBytes
				m.speed = float64(bytesDiff) / elapsed
			}
		}
		m.lastUpdate = now
		m.lastDownloadedBytes = m.downloadedBytes

		if m.done {
			return m, tea.Quit
		}
		return m, tickSpeed()

	case doneMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	}

	return m, nil
}

func (m downloadModel) View() string {
	if m.err != nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Render(fmt.Sprintf("✗ Error: %v\n", m.err))
	}

	if m.done {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")).
			Render(fmt.Sprintf("✓ Downloaded: %s\n", m.filename))
	}

	progressBar := m.progress.View()

	// Format bytes
	downloaded := formatBytes(m.downloadedBytes)
	total := formatBytes(m.totalBytes)
	speed := formatBytes(int64(m.speed)) + "/s"

	// Calculate ETA
	eta := ""
	if m.speed > 0 && m.totalBytes > 0 {
		remaining := m.totalBytes - m.downloadedBytes
		etaSeconds := float64(remaining) / m.speed
		eta = formatDuration(time.Duration(etaSeconds * float64(time.Second)))
	}

	info := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render(fmt.Sprintf("%s / %s  •  %s  •  ETA: %s", downloaded, total, speed, eta))

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("86")).
		Render(fmt.Sprintf("📥 %s", m.filename))

	return fmt.Sprintf("\n%s\n%s\n%s\n", title, progressBar, info)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "∞"
	}
	if d < time.Second {
		return "< 1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// progressWriter tracks writes and sends progress updates
type progressWriter struct {
	written int64
	program *tea.Program
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.written += int64(n)
	pw.program.Send(progressMsg{bytes: pw.written})
	return n, nil
}
