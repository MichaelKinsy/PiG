package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkThemeWatcherSelection(b *testing.B) {
	directory := b.TempDir()
	data, err := builtinThemes.ReadFile("theme_dark.json")
	if err != nil {
		b.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		b.Fatal(err)
	}
	value["name"] = "custom-test"
	data, err = json.Marshal(value)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "custom-test.json"), data, 0o600); err != nil {
		b.Fatal(err)
	}
	previousRegistry, previousTheme := ActiveThemeRegistry(), ActiveTheme()
	b.Cleanup(func() {
		SetThemeRegistry(previousRegistry)
		storeActiveTheme(previousTheme)
		noteSelectedTheme(previousTheme.Name, false)
	})
	registry := NewThemeRegistry()
	if err := registry.LoadDir(directory); err != nil {
		b.Fatal(err)
	}
	SetThemeRegistry(registry)
	SetTheme("dark")
	watcher := StartThemeWatcher(b.Context(), directory, nil)
	defer watcher.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		SetThemeByName("custom-test", true)
		SetTheme("dark", true)
	}
	watcher.mu.Lock()
	active := watcher.watcher != nil
	watcher.mu.Unlock()
	if active {
		b.Fatal("builtin selection retained native watcher")
	}
}
