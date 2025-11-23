package main

import (
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
	"github.com/zeebo/blake3"
)

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

// Messages
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

type requestURLPanicMsg error
type requestURLReceivedMsg []byte
type requestURLDoneMsg struct{}
type requestURLGetBodyMsg struct{ resp *http.Response }
type speedTickMsg time.Time

// Model
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
	speed               float64
	lastUpdate          time.Time
	lastDownloadedBytes int64
	nextPercent         float64

	chunkBuf []byte

	// Blake3 verification fields
	blake3Progress   progress.Model
	blake3File       *os.File
	blake3Hasher     *blake3.Hasher
	blake3TotalBytes int64
	blake3ReadBytes  int64
	blake3RemoteHash string
	verifying        bool

	windowWidth  int
	windowHeight int
}

func newDownloadModel(username, fn, partFn, url string, offset int64) downloadModel {
	prog := progress.New(progress.WithDefaultGradient())
	prog.Width = 50
	blake := progress.New(progress.WithDefaultGradient())
	blake.Width = 50
	return downloadModel{
		username:            username,
		filename:            fn,
		partFilename:        partFn,
		URL:                 url,
		progress:            prog,
		downloadedBytes:     offset,
		lastUpdate:          time.Now(),
		lastDownloadedBytes: offset,
		blake3Progress:      blake,
		chunkBuf:            make([]byte, 128*1024),
	}
}

// Commands
func generateRenameFileCmd(partFn, finalFn string) tea.Cmd {
	return func() tea.Msg {
		err := os.Rename(partFn, finalFn)
		return fileRenamedMsg{err: err}
	}
}

// Nouveau message si hash pas prêt
type blake3PendingMsg struct{}

// Message interne pour relancer la requête
type blake3RetryFetchMsg struct{}

func generateFetchBlake3Cmd(url, filename, username string) tea.Cmd {
	return func() tea.Msg {
		blake3URL := url + filename + ".blake3"
		req, err := http.NewRequest("GET", blake3URL, nil)
		if err != nil {
			return blake3FetchedMsg{err: err}
		}
		req.Header.Set("X-PushPop-User", username)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return blake3FetchedMsg{err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusServiceUnavailable {
			return blake3PendingMsg{}
		}
		if resp.StatusCode != http.StatusOK {
			return blake3FetchedMsg{err: fmt.Errorf("unexpected status %d", resp.StatusCode)}
		}

		limitedReader := io.LimitReader(resp.Body, 1024)
		remoteHashBytes, err := io.ReadAll(limitedReader)
		if err != nil {
			return blake3FetchedMsg{err: err}
		}
		remoteHash := stringsTrimSpace(string(remoteHashBytes))
		if len(remoteHash) != 64 {
			return blake3FetchedMsg{err: fmt.Errorf("invalid BLAKE3 hash length: %d", len(remoteHash))}
		}
		return blake3FetchedMsg{remoteHash: remoteHash}
	}
}

func generateComputeBlake3Cmd(filename, remoteHash string) tea.Cmd {
	return func() tea.Msg {
		fi, err := os.Stat(filename)
		if err != nil {
			return blake3ComputedMsg{err: err}
		}
		totalBytes := fi.Size()
		file, err := os.Open(filename)
		if err != nil {
			return blake3ComputedMsg{err: err}
		}
		return blake3StartMsg{file: file, totalBytes: totalBytes, remoteHash: remoteHash}
	}
}

func generateReadBlake3ChunkCmd(file *os.File) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 128*1024)
		n, err := file.Read(buf)
		if n > 0 {
			return blake3ChunkReadMsg{chunk: buf[:n]}
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

func requestURL(m downloadModel) tea.Cmd {
	return func() tea.Msg {
		log.Println("Requesting URL:", m.URL)
		req, err := http.NewRequest("GET", m.URL, nil)
		if err != nil {
			return requestURLPanicMsg(err)
		}
		if m.downloadedBytes > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", m.downloadedBytes))
		}
		req.Header.Set("X-PushPop-User", m.username)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return requestURLPanicMsg(err)
		}
		return requestURLGetBodyMsg{resp: resp}
	}
}

func generateReadChunkCmd(body io.ReadCloser, buf []byte) tea.Cmd {
	return func() tea.Msg {
		n, err := body.Read(buf)
		if n > 0 {
			return requestURLReceivedMsg(buf[:n]) // pas de copie supplémentaire
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

func tickSpeed() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return speedTickMsg(t) })
}

// Init
func (m downloadModel) Init() tea.Cmd {
	return tea.Batch(tickSpeed(), requestURL(m))
}

// Update
func (m downloadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.err = fmt.Errorf("aborted")
			return m, tea.Quit
		}

	case requestURLGetBodyMsg:
		m.Body = msg.resp.Body
		m.totalBytes = msg.resp.ContentLength
		if m.downloadedBytes > 0 && msg.resp.StatusCode == http.StatusPartialContent {
			m.totalBytes += m.downloadedBytes
		}
		return m, generateReadChunkCmd(m.Body, m.chunkBuf)

	case requestURLReceivedMsg:
		f, err := createOrOpenPartFile(m.partFilename)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		defer f.Close()
		chunk := []byte(msg)
		if _, err = f.Write(chunk); err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.downloadedBytes += int64(len(chunk))
		if m.totalBytes > 0 {
			m.nextPercent = float64(m.downloadedBytes) / float64(m.totalBytes)
		}
		return m, generateReadChunkCmd(m.Body, m.chunkBuf)

	case requestURLDoneMsg:
		if m.Body != nil {
			m.Body.Close()
			m.Body = nil
		}
		m.done = true
		return m, generateRenameFileCmd(m.partFilename, m.filename)

	case fileRenamedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("rename: %w", msg.err)
			return m, tea.Quit
		}
		return m, generateFetchBlake3Cmd(m.URL, m.filename, m.username)

	case blake3PendingMsg:
		return m, tea.Tick(1*time.Second, func(time.Time) tea.Msg {
			return blake3RetryFetchMsg{}
		})

	case blake3RetryFetchMsg:
		return m, generateFetchBlake3Cmd(m.URL, m.filename, m.username)

	case blake3FetchedMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("blake3 fetch: %w", msg.err)
			return m, tea.Quit
		}
		return m, generateComputeBlake3Cmd(m.filename, msg.remoteHash)

	case blake3StartMsg:
		if msg.file == nil {
			m.err = fmt.Errorf("open for hash failed")
			return m, tea.Quit
		}
		m.verifying = true
		m.blake3File = msg.file
		m.blake3Hasher = blake3.New()
		m.blake3TotalBytes = msg.totalBytes
		m.blake3ReadBytes = 0
		m.blake3RemoteHash = msg.remoteHash
		m.nextPercent = 0
		return m, generateReadBlake3ChunkCmd(m.blake3File)

	case blake3ChunkReadMsg:
		if msg.err != nil && msg.err != io.EOF {
			if m.blake3File != nil {
				m.blake3File.Close()
			}
			m.err = fmt.Errorf("hash read: %w", msg.err)
			return m, tea.Quit
		}
		if len(msg.chunk) > 0 {
			m.blake3Hasher.Write(msg.chunk)
			m.blake3ReadBytes += int64(len(msg.chunk))
			if m.blake3TotalBytes > 0 {
				m.nextPercent = float64(m.blake3ReadBytes) / float64(m.blake3TotalBytes)
			}
			return m, generateReadBlake3ChunkCmd(m.blake3File)
		}
		if m.blake3File != nil {
			m.blake3File.Close()
			m.blake3File = nil
		}
		localHash := fmt.Sprintf("%x", m.blake3Hasher.Sum(nil))
		return m, func() tea.Msg {
			return blake3ComputedMsg{localHash: localHash, remoteHash: m.blake3RemoteHash}
		}

	case blake3ComputedMsg:
		m.verifying = false
		if msg.err != nil {
			m.err = fmt.Errorf("hash compute: %w", msg.err)
			return m, tea.Quit
		}
		if msg.localHash != msg.remoteHash {
			m.err = fmt.Errorf("BLAKE3 mismatch\nexpected: %s\ngot: %s", msg.remoteHash, msg.localHash)
			_ = os.Remove(m.filename)
			return m, tea.Quit
		}
		return m, tea.Quit

	case requestURLPanicMsg:
		m.err = msg
		return m, tea.Quit

	case progress.FrameMsg:
		if m.verifying {
			pm, cmd := m.blake3Progress.Update(msg)
			m.blake3Progress = pm.(progress.Model)
			return m, cmd
		}
		pm, cmd := m.progress.Update(msg)
		m.progress = pm.(progress.Model)
		return m, cmd

	case speedTickMsg:
		var cmds []tea.Cmd
		now := time.Time(msg)
		if !m.lastUpdate.IsZero() {
			if !m.verifying && !m.done {
				elapsed := now.Sub(m.lastUpdate).Seconds()
				if elapsed > 0 {
					diff := m.downloadedBytes - m.lastDownloadedBytes
					m.speed = float64(diff) / elapsed
				}
				cmds = append(cmds, m.progress.SetPercent(m.nextPercent))
			} else if m.verifying {
				cmds = append(cmds, m.blake3Progress.SetPercent(m.nextPercent))
			}
		}
		if !m.verifying && !m.done {
			m.lastUpdate = now
			m.lastDownloadedBytes = m.downloadedBytes
		}
		if m.verifying || !m.done {
			cmds = append(cmds, tickSpeed())
		}
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.windowWidth = msg.Width
		m.windowHeight = msg.Height

		log.Println("width = ", msg.Width, ", height = ", msg.Height)

		w := msg.Width - 4
		if w < 10 {
			w = 10
		}
		m.progress.Width = w
		m.blake3Progress.Width = w

		if m.verifying {
			return m, m.blake3Progress.SetPercent(m.nextPercent)
		}
		return m, m.progress.SetPercent(m.nextPercent)
	}
	return m, nil
}

// View
func (m downloadModel) View() string {
	if m.err != nil {
		return styleErr(fmt.Sprintf("✗ Error: %v\n", m.err))
	}
	if m.verifying {
		title := styleTitle("🔐 Verifying BLAKE3 integrity...")
		bar := m.blake3Progress.View()
		info := styleInfo(fmt.Sprintf("%s / %s",
			formatBytes(m.blake3ReadBytes),
			formatBytes(m.blake3TotalBytes),
		))
		return fmt.Sprintf("%s\n%s\n%s\n", title, bar, info)
	}
	if m.done && !m.verifying {
		return styleDone("✓ Download complete. Checking integrity...\n")
	}
	title := styleTitle(fmt.Sprintf("📥 %s", m.filename))
	bar := m.progress.View()
	downloaded := formatBytes(m.downloadedBytes)
	total := formatBytes(m.totalBytes)
	speed := formatBytes(int64(m.speed)) + "/s"
	eta := ""
	if m.speed > 0 && m.totalBytes > 0 {
		rem := m.totalBytes - m.downloadedBytes
		etaSecs := float64(rem) / m.speed
		eta = formatDuration(time.Duration(etaSecs * float64(time.Second)))
	}
	info := styleInfo(fmt.Sprintf("%s / %s  •  %s  •  ETA: %s", downloaded, total, speed, eta))
	return fmt.Sprintf("%s\n%s\n%s\n", title, bar, info)
}

// Helpers (styling, formatting)
var (
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render
	styleDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render
	styleInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render
	styleTitleFn = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
)

func styleTitle(s string) string { return styleTitleFn.Render(s) }

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

// Small helper to avoid importing strings just for TrimSpace in multiple spots
func stringsTrimSpace(s string) string {
	return strings.TrimSpace(s)
}
