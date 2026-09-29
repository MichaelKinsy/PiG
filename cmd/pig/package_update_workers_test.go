package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPackageUpdateChecksBoundWorkers(t *testing.T) {
	// Pi package-manager.ts:50,1657-1680 admits five workers, not one task per package parked behind a semaphore.
	const workerLimit = 5
	const taskCount = 2*workerLimit + 2
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	started := make(chan struct{}, taskCount)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		_, _ = io.WriteString(w, `"2.0.0"`)
	}))
	defer server.Close()
	t.Setenv("PIG_PACKAGE_UPDATE_WORKER_URL", server.URL)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agent)
	if err := settings.SetNpmCommand([]string{executable, "-test.run=^TestPackageUpdateWorkerCommand$", "--"}); err != nil {
		t.Fatal(err)
	}
	packages := make([]codingagent.PackageSource, taskCount)
	for i := range packages {
		name := fmt.Sprintf("worker-package-%d", i)
		packages[i].Source = "npm:" + name
		writeStartupFixtureFile(t, filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "npm", "node_modules", name, "package.json"), `{"version":"1.0.0"}`)
	}
	if err := settings.SetProjectPackages(packages); err != nil {
		t.Fatal(err)
	}
	result := make(chan []PackageUpdate, 1)
	go func() { result <- CheckForAvailableUpdates(cwd, settings) }()
	for range workerLimit {
		<-started
	}
	// Count only goroutines owned by this production operation; HTTP and os/exec runtime goroutines are not part of the worker budget.
	count := packageUpdateOwnedGoroutines()
	if count < 1 || count > workerLimit+1 {
		t.Errorf("update-check goroutines=%d, want at most %d workers plus the caller", count, workerLimit)
	}
	close(release)
	updates := <-result
	if count := packageUpdateOwnedGoroutines(); count != 0 {
		t.Errorf("update check retained %d owned goroutines after return", count)
	}
	if len(updates) != len(packages) {
		t.Fatalf("updates=%d, want one for each of %d configured packages", len(updates), len(packages))
	}
	for i, update := range updates {
		if update.Source != packages[i].Source {
			t.Fatalf("update %d source=%q, want %q", i, update.Source, packages[i].Source)
		}
	}
}

func packageUpdateOwnedGoroutines() int {
	var records []runtime.StackRecord
	for {
		n, ok := runtime.GoroutineProfile(records)
		if !ok {
			records = make([]runtime.StackRecord, n)
			continue
		}
		records = records[:n]
		break
	}
	count := 0
	for _, record := range records {
		frames := runtime.CallersFrames(record.Stack())
		for {
			frame, more := frames.Next()
			if strings.Contains(frame.Function, "/cmd/pig.CheckForAvailableUpdates") {
				count++
				break
			}
			if !more {
				break
			}
		}
	}
	return count
}

func TestPackageUpdateWorkerCommand(t *testing.T) {
	endpoint := os.Getenv("PIG_PACKAGE_UPDATE_WORKER_URL")
	if endpoint == "" {
		return
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		t.Fatal("worker fixture requires its loopback HTTP server")
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil) //nolint:gosec // G704: test subprocess contacts only the parent's loopback fixture, validated above.
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request) //nolint:gosec // G704: test-only loopback fixture; no user-controlled or external request.
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
}
