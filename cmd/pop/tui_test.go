package main

import (
	"os"
	"path/filepath"
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
