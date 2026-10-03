package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeWatchedTheme(t *testing.T, directory, name, accent string) {
	t.Helper()
	data, err := builtinThemes.ReadFile("theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["name"] = name
	if accent != "" {
		value["colors"].(map[string]any)["accent"] = accent
	}
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// upstream: packages/coding-agent/test/suite/regressions/2791-fswatch-error-crash.test.ts:41
func TestThemeWatcherProcessSurvivesErrorUpstream(t *testing.T) {
	if os.Getenv("PIG_THEME_WATCHER_CHILD") == "1" {
		dir := os.Getenv("PIG_THEME_WATCHER_DIR")
		registry := NewThemeRegistry()
		if err := registry.LoadDir(dir); err != nil {
			t.Fatal(err)
		}
		SetThemeRegistry(registry)
		SetThemeByName("custom-test")
		watcher := StartThemeWatcher(t.Context(), dir, nil)
		defer watcher.Close()
		watcher.mu.Lock()
		native := watcher.watcher
		watcher.mu.Unlock()
		if native == nil {
			t.Fatal("no native theme watcher")
		}
		// An unhandled error cannot complete this send. The parent bounds and reaps the whole child, matching the upstream process-survival regression.
		native.Errors <- errors.New("simulated OS watcher failure")
		watcher.Close()
		fmt.Println("THEME_WATCHER_ERROR survived")
		return
	}
	dir := filepath.Join(t.TempDir(), "agent", "themes")
	writeWatchedTheme(t, dir, "custom-test", "")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestThemeWatcherProcessSurvivesErrorUpstream$")
	child.Env = append(os.Environ(), "PIG_THEME_WATCHER_CHILD=1", "PIG_THEME_WATCHER_DIR="+dir)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("child crashed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "THEME_WATCHER_ERROR survived") {
		t.Fatalf("error path not exercised: %s", output)
	}
	fmt.Println("THEME_WATCHER_ERROR survived")
}

func watchedThemeFixture(t *testing.T) (string, *ThemeWatcher) {
	t.Helper()
	oldRegistry, oldTheme := ActiveThemeRegistry(), ActiveTheme()
	t.Cleanup(func() {
		SetThemeRegistry(oldRegistry)
		storeActiveTheme(oldTheme)
		noteSelectedTheme(oldTheme.Name, false)
	})
	dir := t.TempDir()
	writeWatchedTheme(t, dir, "custom-test", "#123456")
	writeWatchedTheme(t, dir, "other-test", "#abcdef")
	registry := NewThemeRegistry()
	if err := registry.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	SetThemeRegistry(registry)
	SetThemeByName("custom-test")
	watcher := StartThemeWatcher(t.Context(), dir, nil)
	t.Cleanup(watcher.Close)
	return dir, watcher
}
func waitThemeAccent(t *testing.T, want string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if ActiveTheme().Colors()["accent"] == want {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("accent=%q want=%q", ActiveTheme().Colors()["accent"], want)
		}
	}
}

// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:790-806 — a preview with enableWatcher=false changes the theme without replacing the active native registration.
func TestThemeWatcherPreviewDoesNotReplaceRegistration(t *testing.T) {
	dir, watcher := watchedThemeFixture(t)
	watcher.mu.Lock()
	original, generation := watcher.watcher, watcher.generation
	watcher.mu.Unlock()
	SetThemeByName("other-test")
	watcher.mu.Lock()
	unchanged := watcher.watcher == original && watcher.name == "custom-test" && watcher.generation == generation
	watcher.mu.Unlock()
	if !unchanged {
		t.Fatal("preview replaced the native registration")
	}
	before := ActiveTheme()
	watcher.reload("custom-test", generation)
	if ActiveTheme() != before {
		t.Fatal("inactive watched theme overwrote the preview")
	}
	SetThemeByName("custom-test")
	writeWatchedTheme(t, dir, "custom-test", "#203040")
	waitThemeAccent(t, "#203040")
}

func TestThemeWatcherReloadsAfterAtomicReplacement(t *testing.T) {
	dir, _ := watchedThemeFixture(t)
	writeWatchedTheme(t, dir, "replacement", "#654321")
	if err := os.Rename(filepath.Join(dir, "replacement.json"), filepath.Join(dir, "custom-test.json")); err != nil {
		t.Fatal(err)
	}
	waitThemeAccent(t, "#654321")
	if got := ActiveThemeRegistry().Get("custom-test").Colors()["accent"]; got != "#654321" {
		t.Fatalf("registry cache=%s", got)
	}
	// The JSON name may differ after replacement; the watcher remains attached to the selected filename, as in Pi.
	writeWatchedTheme(t, dir, "custom-test", "#102030")
	waitThemeAccent(t, "#102030")
	fmt.Println("THEME_WATCHER_RELOAD #654321 #102030")
}

// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:864,449 — reloading replaces the registered theme's sourcePath as well as its colors.
func TestThemeWatcherReloadUpdatesRegisteredSourcePath(t *testing.T) {
	dir, _ := watchedThemeFixture(t)
	other := t.TempDir()
	writeWatchedTheme(t, other, "custom-test", "#123456")
	path := filepath.Join(other, "custom-test.json")
	theme, err := LoadThemeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ActiveThemeRegistry().AddFile(theme, path)
	SetThemeByName("custom-test", true)
	writeWatchedTheme(t, dir, "custom-test", "#304050")
	waitThemeAccent(t, "#304050")
	if got, want := ActiveThemeRegistry().PathOf("custom-test"), filepath.Join(dir, "custom-test.json"); got != want {
		t.Fatalf("reloaded source=%q want=%q", got, want)
	}
}

func TestThemeWatcherQueuedReloadCannotOverwriteReplacementOrShutdown(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			dir, watcher := watchedThemeFixture(t)
			actions := make(chan func(), 1)
			watcher.mu.Lock()
			watcher.dispatch = func(ctx context.Context, action func()) error {
				select {
				case actions <- action:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			watcher.mu.Unlock()
			writeWatchedTheme(t, dir, "custom-test", "#654321")
			var apply func()
			select {
			case apply = <-actions:
			case <-time.After(5 * time.Second):
				t.Fatal("reload was not dispatched")
			}
			if stop {
				watcher.Close()
			} else {
				SetThemeByName("other-test", true)
			}
			before := ActiveTheme()
			apply()
			if ActiveTheme() != before {
				t.Fatal("stale reload changed the active theme")
			}
		})
	}
	fmt.Println("THEME_WATCHER_STALE replacement/stop retained")
}

func TestThemeWatcherKeepsLastGoodAndCoalescesBurst(t *testing.T) {
	dir, watcher := watchedThemeFixture(t)
	original := ActiveTheme()
	watcher.mu.Lock()
	generation := watcher.generation
	watcher.mu.Unlock()
	path := filepath.Join(dir, "custom-test.json")
	if err := os.WriteFile(path, []byte(`{"name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	watcher.reload("custom-test", generation)
	if ActiveTheme() != original {
		t.Fatal("invalid file replaced last good theme")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	watcher.reload("custom-test", generation)
	if ActiveTheme() != original {
		t.Fatal("missing file replaced last good theme")
	}
	for i := range 100 {
		writeWatchedTheme(t, dir, "custom-test", fmt.Sprintf("#%06x", i+1))
	}
	waitThemeAccent(t, "#000064")
}

func TestThemeWatcherCloseCancelsBlockedDispatch(t *testing.T) {
	dir, watcher := watchedThemeFixture(t)
	started := make(chan struct{})
	watcher.mu.Lock()
	watcher.dispatch = func(ctx context.Context, _ func()) error { close(started); <-ctx.Done(); return ctx.Err() }
	watcher.mu.Unlock()
	writeWatchedTheme(t, dir, "custom-test", "#654321")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("reload not dispatched")
	}
	done := make(chan struct{})
	go func() { watcher.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel and join blocked dispatch")
	}
}

func TestThemeWatcherErrorClosesNotificationsAndBuiltinsDoNotWatch(t *testing.T) {
	dir, watcher := watchedThemeFixture(t)
	watcher.mu.Lock()
	native := watcher.watcher
	watcher.mu.Unlock()
	if native == nil {
		t.Fatal("native watcher not installed")
	}
	native.Errors <- errors.New("simulated OS watcher failure")
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case _, open := <-native.Events:
		if open {
			t.Fatal("watcher did not close after error")
		}
	case <-deadline.C:
		t.Fatal("watcher error left native notifications alive")
	}
	SetThemeByName("custom-test", true)
	writeWatchedTheme(t, dir, "custom-test", "#654321")
	waitThemeAccent(t, "#654321")
	SetThemeByName("dark", true)
	watcher.mu.Lock()
	watching := watcher.watcher != nil
	watcher.mu.Unlock()
	if watching {
		t.Fatal("builtin theme retained a native watcher")
	}
	watcher.Close()
	select {
	case <-watcher.done:
	default:
		t.Fatal("Close did not join its worker")
	}
}

// upstream 0.99.1 theme.ts:812-818 startThemeWatcher: the system theme is built-in, so a same-named file in the custom directory is not watched.
func TestThemeWatcherSkipsTheSystemTheme(t *testing.T) {
	dir := t.TempDir()
	writeWatchedTheme(t, dir, SystemThemeName, "")
	writeWatchedTheme(t, dir, "custom-test", "")
	for name, watched := range map[string]bool{SystemThemeName: false, "custom-test": true} {
		watcher := StartThemeWatcher(t.Context(), dir, nil)
		watcher.selectTheme(name)
		watcher.mu.Lock()
		native := watcher.watcher
		watcher.mu.Unlock()
		if (native != nil) != watched {
			t.Errorf("theme %q watched = %v, want %v", name, native != nil, watched)
		}
		watcher.Close()
	}
}
