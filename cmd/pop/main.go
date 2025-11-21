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
	"github.com/yifu/pushpop/pkg/discovery"
	"github.com/zeebo/blake3"
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
		blake3Progress:      progress.New(progress.WithDefaultGradient()),
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
		log.Fatalln("Error:", dm.err)
	}

	fmt.Println("✓ Download complete and verified:", fn)
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

// New message types for post-download workflow
type fileRenamedMsg struct{ err error }
type blake3FetchedMsg struct {
	remoteHash string
	err        error
}
type blake3StartMsg struct {
	file       *os.File
	totalBytes int64
	remoteHash string
}
type blake3ChunkReadMsg struct {
	chunk []byte
	err   error
}
type blake3ComputedMsg struct {
	localHash  string
	remoteHash string
	err        error
}

// Commands for post-download workflow
func generateRenameFileCmd(partFn, finalFn string) tea.Cmd {
	return func() tea.Msg {
		err := os.Rename(partFn, finalFn)
		return fileRenamedMsg{err: err}
	}
}

func generateFetchBlake3Cmd(url, filename string) tea.Cmd {
	return func() tea.Msg {
		blake3URL := url + filename + ".blake3"
		resp, err := http.Get(blake3URL)
		if err != nil {
			return blake3FetchedMsg{err: err}
		}
		defer resp.Body.Close()

		// Limit read to 1KB to protect against malicious server
		limitedReader := io.LimitReader(resp.Body, 1024)
		remoteHashBytes, err := io.ReadAll(limitedReader)
		if err != nil {
			return blake3FetchedMsg{err: err}
		}

		remoteHash := strings.TrimSpace(string(remoteHashBytes))
		// Validate hash format (should be 64 hex chars)
		if len(remoteHash) != 64 {
			return blake3FetchedMsg{err: fmt.Errorf("invalid BLAKE3 hash format (expected 64 chars, got %d)", len(remoteHash))}
		}

		return blake3FetchedMsg{remoteHash: remoteHash}
	}
}

func generateComputeBlake3Cmd(filename, remoteHash string) tea.Cmd {
	return func() tea.Msg {
		// Get file size for progress
		fi, err := os.Stat(filename)
		if err != nil {
			return blake3ComputedMsg{err: err}
		}
		totalBytes := fi.Size()

		// Open file
		file, err := os.Open(filename)
		if err != nil {
			return blake3ComputedMsg{err: err}
		}

		return blake3StartMsg{
			file:       file,
			totalBytes: totalBytes,
			remoteHash: remoteHash,
		}
	}
}

func generateReadBlake3ChunkCmd(file *os.File) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 32*1024) // 32KB chunks
		n, err := file.Read(buf)

		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			return blake3ChunkReadMsg{chunk: chunk}
		}

		if err == io.EOF {
			return blake3ChunkReadMsg{err: io.EOF}
		}

		if err != nil {
			return blake3ChunkReadMsg{err: err}
		}

		return blake3ChunkReadMsg{err: io.EOF}
	}
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
	nextPercent         float64

	// Blake3 verification fields
	blake3Progress   progress.Model
	blake3File       *os.File
	blake3Hasher     *blake3.Hasher
	blake3TotalBytes int64
	blake3ReadBytes  int64
	blake3RemoteHash string
	verifying        bool
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

		m.downloadedBytes += int64(len(chunk))
		if m.totalBytes > 0 {
			m.nextPercent = float64(m.downloadedBytes) / float64(m.totalBytes)
		}

		return m, readChunk(m.Body)

	case requestURLDoneMsg:
		if m.Body != nil {
			m.Body.Close()
			m.Body = nil
		}
		// Download complete, start post-download workflow
		m.done = true
		return m, generateRenameFileCmd(m.partFilename, m.filename)

	case fileRenamedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("rename error: %w", msg.err)
			return m, tea.Quit
		}
		// File renamed successfully, now fetch BLAKE3 hash
		return m, generateFetchBlake3Cmd(m.URL, m.filename)

	case blake3FetchedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("unable to retrieve BLAKE3 hash: %w", msg.err)
			return m, tea.Quit
		}
		// Hash fetched, start computing local hash
		return m, generateComputeBlake3Cmd(m.filename, msg.remoteHash)

	case blake3StartMsg:
		if msg.file == nil {
			m.err = fmt.Errorf("failed to open file for hashing")
			return m, tea.Quit
		}
		// Initialize BLAKE3 computation
		m.verifying = true
		m.blake3File = msg.file
		m.blake3Hasher = blake3.New()
		m.blake3TotalBytes = msg.totalBytes
		m.blake3ReadBytes = 0
		m.blake3RemoteHash = msg.remoteHash

		// Start reading first chunk
		return m, generateReadBlake3ChunkCmd(m.blake3File)

	case blake3ChunkReadMsg:
		if msg.err != nil && msg.err != io.EOF {
			// Real error (not EOF)
			if m.blake3File != nil {
				m.blake3File.Close()
			}
			m.err = fmt.Errorf("error reading file for hash: %w", msg.err)
			return m, tea.Quit
		}

		if len(msg.chunk) > 0 {
			// Process chunk
			m.blake3Hasher.Write(msg.chunk)
			m.blake3ReadBytes += int64(len(msg.chunk))

			// Update progress (store target percent; animation triggered by speedTickMsg)
			if m.blake3TotalBytes > 0 {
				m.nextPercent = float64(m.blake3ReadBytes) / float64(m.blake3TotalBytes)
			}

			// Continue reading next chunk
			return m, generateReadBlake3ChunkCmd(m.blake3File)
		}

		// EOF reached, finalize hash
		if m.blake3File != nil {
			m.blake3File.Close()
			m.blake3File = nil
		}

		localHash := fmt.Sprintf("%x", m.blake3Hasher.Sum(nil))
		return m, func() tea.Msg {
			return blake3ComputedMsg{
				localHash:  localHash,
				remoteHash: m.blake3RemoteHash,
			}
		}

	case blake3ComputedMsg:
		m.verifying = false
		if msg.err != nil {
			m.err = fmt.Errorf("error computing local hash: %w", msg.err)
			return m, tea.Quit
		}
		// Compare hashes
		if msg.localHash != msg.remoteHash {
			m.err = fmt.Errorf("file integrity check failed (BLAKE3 mismatch)\nexpected: %s\nobtained: %s", msg.remoteHash, msg.localHash)
			// Delete corrupted file
			os.Remove(m.filename)
			return m, tea.Quit
		}
		// All good!
		return m, tea.Quit

	case requestURLPanicMsg:
		m.err = msg.(error)
		return m, tea.Quit

	case progress.FrameMsg:
		if m.verifying {
			// Update BLAKE3 progress bar
			progressModel, cmd := m.blake3Progress.Update(msg)
			m.blake3Progress = progressModel.(progress.Model)
			return m, cmd
		}
		// Update download progress bar
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd

	case speedTickMsg:
		var cmds []tea.Cmd

		now := time.Time(msg)

		if !m.lastUpdate.IsZero() {
			if !m.verifying && !m.done {
				// Download phase: compute speed and drive download progress bar
				elapsed := now.Sub(m.lastUpdate).Seconds()
				if elapsed > 0 {
					bytesDiff := m.downloadedBytes - m.lastDownloadedBytes
					m.speed = float64(bytesDiff) / elapsed
				}
				cmds = append(cmds, m.progress.SetPercent(m.nextPercent))
			} else if m.verifying {
				// Verifying phase: drive BLAKE3 progress bar
				cmds = append(cmds, m.blake3Progress.SetPercent(m.nextPercent))
			}
		}

		// Update download counters only during download
		if !m.verifying && !m.done {
			m.lastUpdate = now
			m.lastDownloadedBytes = m.downloadedBytes
		}

		// Keep ticking during verifying or download
		if m.verifying || !m.done {
			cmds = append(cmds, tickSpeed())
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m downloadModel) View() string {
	if m.err != nil {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Render(fmt.Sprintf("✗ Error: %v\n", m.err))
	}

	// If verifying BLAKE3
	if m.verifying {
		title := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("214")).
			Render("🔐 Verifying BLAKE3 integrity...")

		progressBar := m.blake3Progress.View()

		verified := formatBytes(m.blake3ReadBytes)
		total := formatBytes(m.blake3TotalBytes)
		info := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Render(fmt.Sprintf("%s / %s", verified, total))

		return fmt.Sprintf("\n%s\n%s\n%s\n", title, progressBar, info)
	}

	// If download complete but not yet verified
	if m.done && !m.verifying {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")).
			Render(fmt.Sprintf("✓ Download complete, verifying...\n"))
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
