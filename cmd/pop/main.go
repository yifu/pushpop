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
	defer cancel()

	// Structure to hold discovered service info
	type serviceInfo struct {
		url      string
		filename string
	}
	foundService := make(chan serviceInfo, 1)

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

			// Send found service info and exit goroutine
			foundService <- serviceInfo{url: url, filename: fn}
			return
		}
		log.Println("No more entries.")
		close(foundService)
	}(entries)

	err = resolver.Browse(ctx, "_pushpop._tcp", "local.", entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	// Wait for service discovery
	service, ok := <-foundService
	if !ok {
		log.Fatalln("No service found for user:", username)
	}

	url := service.url
	fn := service.filename
	partFn := fn + ".part"

	// Check if final file exists
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

	// Check if .part file exists for resume
	var offset int64 = 0
	if fileExists(partFn) {
		fi, err := os.Stat(partFn)
		if err == nil {
			offset = fi.Size()
		}
	}

	// Create Bubble Tea model for progress
	prog := progress.New(progress.WithDefaultGradient())
	prog.Width = 50

	model := downloadModel{
		username:            username,
		filename:            fn,
		partFilename:        partFn,
		URL:                 url,
		progress:            prog,
		downloadedBytes:     offset,
		lastUpdate:          time.Now(),
		lastDownloadedBytes: offset,
	}

	p := tea.NewProgram(model)

	// Run Bubble Tea UI in main thread (blocks until done)
	finalModel, err := p.Run()
	if err != nil {
		log.Fatalln("Error running progress UI:", err)
	}

	// Check if download had errors
	dm := finalModel.(downloadModel)
	if dm.err != nil {
		log.Fatalln("download error:", dm.err)
	}

	// Rename .part to final name
	err = os.Rename(partFn, fn)
	if err != nil {
		log.Fatalln("rename error:", err)
	}
	fmt.Printf("Download complete: %s\n", fn)

	// BLAKE3 integrity verification
	blake3URL := url + fn + ".blake3"
	respHash, err := http.Get(blake3URL)
	if err != nil {
		log.Fatalf("Unable to retrieve BLAKE3 hash: %v", err)
	}
	defer respHash.Body.Close()
	remoteHashBytes, err := io.ReadAll(respHash.Body)
	if err != nil {
		log.Fatalf("Error reading remote hash: %v", err)
	}
	remoteHash := strings.TrimSpace(string(remoteHashBytes))

	// Compute local hash
	localHash, err := blake.CalcBlake3(fn)
	if err != nil {
		log.Fatalf("Error computing local hash: %v", err)
	}
	if localHash != remoteHash {
		log.Printf("ERROR: file integrity check failed (BLAKE3 mismatch)\nexpected: %s\nobtained: %s", remoteHash, localHash)
		// Delete corrupted file
		err := os.Remove(fn)
		if err != nil {
			log.Printf("Unable to delete corrupted file: %v", err)
		}
		os.Exit(1)
	}
	fmt.Println("BLAKE3 integrity check OK.")
}

// fileExists returns true if the file exists and is not a directory.
func fileExists(name string) bool {
	fi, err := os.Stat(name)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

func createOrOpenPartFile(partFn string) (*os.File, error) {
	if fileExists(partFn) {
		return os.OpenFile(partFn, os.O_WRONLY|os.O_APPEND, 0644)
	}
	return os.Create(partFn)
}

// Bubble Tea model for download progress
type downloadModel struct {
	username            string
	filename            string
	partFilename        string
	URL                 string
	Body                io.ReadCloser
	progress            progress.Model
	totalBytes          int64
	downloadedBytes     int64
	err                 error
	done                bool
	speed               float64 // bytes per second
	lastUpdate          time.Time
	lastDownloadedBytes int64
}

type speedTickMsg time.Time

func (m downloadModel) Init() tea.Cmd {

	return tea.Batch(tickSpeed(), requestURL(m))
}

type requestURLPanicMsg error
type requestURLReceivedMsg []byte
type requestURLDoneMsg struct{}
type requestURLGetBodyMsg struct{ resp *http.Response }

func requestURL(downloadModel downloadModel) tea.Cmd {
	return func() tea.Msg {
		// Prepare HTTP request (with Range if resuming)
		req, err := http.NewRequest("GET", downloadModel.URL, nil)
		if err != nil {
			log.Fatalln("http request error:", err)
		}
		if downloadModel.downloadedBytes > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", downloadModel.downloadedBytes))
		}
		// Send username in custom header for server-side logging
		req.Header.Set("X-PushPop-User", downloadModel.username)
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalln("http get error:", err)
		}

		return requestURLGetBodyMsg{resp}
	}
}

func tickSpeed() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return speedTickMsg(t)
	})
}

func readChunk(body io.ReadCloser) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 4096)
		n, err := body.Read(buf)

		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			return requestURLReceivedMsg(chunk)
		}

		if err == io.EOF {
			return requestURLDoneMsg{}
		}

		if err != nil {
			return requestURLPanicMsg(err)
		}

		return requestURLDoneMsg{}
	}
}

func (m downloadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.err = fmt.Errorf("download aborted by user")
			return m, tea.Quit
		}

	case requestURLGetBodyMsg:
		m.Body = msg.resp.Body

		// Get total size for progress bar
		m.totalBytes = msg.resp.ContentLength
		if m.downloadedBytes > 0 && msg.resp.StatusCode == http.StatusPartialContent {
			m.totalBytes += m.downloadedBytes // Add the already downloaded part
		}

		return m, readChunk(m.Body)

	case requestURLReceivedMsg:
		f, err := createOrOpenPartFile(m.partFilename)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		defer f.Close()

		chunk := []byte(msg)
		_, err = f.Write(chunk)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}

		var cmds []tea.Cmd

		m.downloadedBytes += int64(len(chunk))
		if m.totalBytes > 0 {
			percent := float64(m.downloadedBytes) / float64(m.totalBytes)
			cmd := m.progress.SetPercent(percent)
			cmds = append(cmds, cmd)
		}

		cmds = append(cmds, readChunk(m.Body))

		return m, tea.Batch(cmds...)

	case requestURLDoneMsg:
		if m.Body != nil {
			m.Body.Close()
			m.Body = nil
		}
		return m, nil

	case requestURLPanicMsg:
		m.err = msg.(error)
		return m, tea.Quit

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
