package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// newTestModel returns a model whose .part file lives in a temp dir and
// already holds offset bytes.
func newTestModel(t *testing.T, offset int64) downloadModel {
	t.Helper()
	dir := t.TempDir()
	fn := filepath.Join(dir, "file.bin")
	partFn := fn + ".part"
	if offset > 0 {
		if err := os.WriteFile(partFn, make([]byte, offset), 0644); err != nil {
			t.Fatalf("cannot seed .part file: %v", err)
		}
	}
	return newDownloadModel("alice", fn, partFn, "http://127.0.0.1:1/", offset)
}

func bodyMsg(status int, contentLength int64, body string) requestURLGetBodyMsg {
	return requestURLGetBodyMsg{resp: &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		ContentLength: contentLength,
		Body:          io.NopCloser(strings.NewReader(body)),
	}}
}

// The tick must keep running between the end of the download and the start of
// the verification, otherwise the BLAKE3 progress bar never gets a SetPercent
// and stays at 0%.
func TestSpeedTickSurvivesGapBetweenDownloadAndVerification(t *testing.T) {
	m := newTestModel(t, 0)
	m.done = true
	m.verifying = false

	_, cmd := m.Update(speedTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tick was not rescheduled while done && !verifying")
	}
	if _, ok := cmd().(speedTickMsg); !ok {
		t.Fatalf("expected the command to produce a speedTickMsg, got %T", cmd())
	}
}

func TestSpeedTickReschedulesWhileVerifying(t *testing.T) {
	m := newTestModel(t, 0)
	m.done = true
	m.verifying = true

	_, cmd := m.Update(speedTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tick was not rescheduled while verifying")
	}
	msgs := collect(cmd)
	if !hasTick(msgs) {
		t.Fatalf("expected a speedTickMsg among %v", msgs)
	}
}

func TestResumeKeepsPartialFileOn206(t *testing.T) {
	m := newTestModel(t, 10)

	next, _ := m.Update(bodyMsg(http.StatusPartialContent, 90, ""))
	dm := next.(downloadModel)

	if dm.downloadedBytes != 10 {
		t.Fatalf("expected to keep the 10 downloaded bytes, got %d", dm.downloadedBytes)
	}
	if dm.totalBytes != 100 {
		t.Fatalf("expected totalBytes 100 (90 remaining + 10 held), got %d", dm.totalBytes)
	}
	if dm.warning != "" {
		t.Fatalf("unexpected warning on a successful resume: %q", dm.warning)
	}
	if !fileExists(dm.partFilename) {
		t.Fatal("the .part file should have been kept")
	}
}

// A server that ignores Range answers 200 with the whole file; appending it
// behind the bytes we already hold would corrupt the download.
func TestResumeRestartsWhenServerIgnoresRange(t *testing.T) {
	m := newTestModel(t, 10)

	next, _ := m.Update(bodyMsg(http.StatusOK, 100, ""))
	dm := next.(downloadModel)

	if dm.downloadedBytes != 0 {
		t.Fatalf("expected the offset to be reset, got %d", dm.downloadedBytes)
	}
	if dm.totalBytes != 100 {
		t.Fatalf("expected totalBytes 100, got %d", dm.totalBytes)
	}
	if dm.warning == "" {
		t.Fatal("the user should have been warned that resuming was not possible")
	}
	if fileExists(dm.partFilename) {
		t.Fatal("the stale .part file should have been discarded")
	}
	if dm.err != nil {
		t.Fatalf("restarting is not an error: %v", dm.err)
	}
}

// A .part file at least as large as the remote file makes the server answer
// 416; its error page must never be appended to the download.
func TestResumeRestartsOnRangeNotSatisfiable(t *testing.T) {
	m := newTestModel(t, 10)

	next, cmd := m.Update(bodyMsg(http.StatusRequestedRangeNotSatisfiable, 42, "invalid Range"))
	dm := next.(downloadModel)

	if dm.downloadedBytes != 0 {
		t.Fatalf("expected the offset to be reset, got %d", dm.downloadedBytes)
	}
	if fileExists(dm.partFilename) {
		t.Fatal("the oversized .part file should have been discarded")
	}
	if dm.warning == "" {
		t.Fatal("the user should have been warned about the stale .part file")
	}
	if dm.err != nil {
		t.Fatalf("a 416 on resume is recoverable, got error: %v", dm.err)
	}
	if cmd == nil {
		t.Fatal("expected the request to be reissued without a Range header")
	}
}

func TestUnexpectedStatusAborts(t *testing.T) {
	m := newTestModel(t, 0)

	next, _ := m.Update(bodyMsg(http.StatusNotFound, 0, "nope"))
	dm := next.(downloadModel)

	if dm.err == nil {
		t.Fatal("expected an error on an unexpected HTTP status")
	}
}

// collect runs a command and flattens the messages a tea.Batch produces.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, collect(c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

func hasTick(msgs []tea.Msg) bool {
	for _, msg := range msgs {
		if _, ok := msg.(speedTickMsg); ok {
			return true
		}
	}
	return false
}
