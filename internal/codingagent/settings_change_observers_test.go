package codingagent

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// pi.getSettings() reads the live settings (loader.ts:411-414), and a runtime replicates them: the manager reports each change of the effective settings, whether a setter, a reload, a trust change or an override made it, and a reader on another goroutine sees the new settings once the report returns.
func TestSettingsManagerReportsEveryChangeOfTheEffectiveSettings(t *testing.T) {
	sm := NewInMemorySettingsManager(Settings{})
	var reports atomic.Int64
	stop := sm.ObserveChange(func() { reports.Add(1) })
	for _, step := range []struct {
		name   string
		change func()
	}{
		{"a setter", func() { _ = sm.SetEditorPaddingX(2) }},
		{"an override", func() { sm.ApplyOverrides(Settings{Theme: "light"}) }},
		{"a reload", func() { sm.Reload() }},
		{"a trust change", func() { sm.SetProjectTrusted(false) }},
	} {
		before := reports.Load()
		step.change()
		if reports.Load() == before {
			t.Errorf("%s changed the settings without a report", step.name)
		}
	}
	stop()
	before := reports.Load()
	_ = sm.SetEditorPaddingX(1)
	if reports.Load() != before {
		t.Fatal("a stopped observer was told of a change")
	}
}

// The report runs with the manager's lock held, so a reader on another goroutine waits for it and then reads the new settings: the push a runtime gets carries them.
func TestSettingsManagerReportIsFollowedByTheNewSettings(t *testing.T) {
	sm := NewInMemorySettingsManager(Settings{})
	reported := make(chan struct{}, 1)
	sm.ObserveChange(func() {
		select {
		case reported <- struct{}{}:
		default:
		}
	})
	read := make(chan string, 1)
	go func() {
		<-reported
		read <- string(sm.ExtensionSettingsJSON())
	}()
	if err := sm.SetEditorPaddingX(2); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-read:
		var settings map[string]json.RawMessage
		if err := json.Unmarshal([]byte(got), &settings); err != nil {
			t.Fatal(err)
		}
		if string(settings["editorPaddingX"]) != "2" {
			t.Fatalf("settings read after the report = %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never ran")
	}
}
