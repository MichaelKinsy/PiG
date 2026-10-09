package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/bytesize"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func runExtensionsCommand(args []string) int {
	if len(args) == 0 || args[0] != "extensions" {
		return -1
	}
	if len(args) < 3 || args[1] != "cache" {
		printExtensionsCacheHelp()
		return 2
	}
	command := args[2]
	if command != "stats" && command != "prune" {
		printExtensionsCacheHelp()
		return 2
	}
	jsonOutput, dryRun, failures := false, false, false
	retention := 30 * 24 * time.Hour
	var maxSize *int64
	for index := 3; index < len(args); index++ {
		switch args[index] {
		case "--json":
			jsonOutput = true
		case "--dry-run":
			if command != "prune" {
				fmt.Fprintln(os.Stderr, "pig extensions cache stats: --dry-run is only valid for prune")
				return 2
			}
			dryRun = true
		case "--failures":
			if command != "prune" {
				fmt.Fprintln(os.Stderr, "pig extensions cache stats: --failures is only valid for prune")
				return 2
			}
			failures = true
		case "--retention":
			if command != "prune" || index+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "pig extensions cache: --retention requires a duration on prune")
				return 2
			}
			index++
			parsed, err := time.ParseDuration(args[index])
			if err != nil || parsed <= 0 {
				fmt.Fprintf(os.Stderr, "pig extensions cache prune: invalid retention %q\n", args[index])
				return 2
			}
			retention = parsed
		case "--max-size":
			if command != "prune" || index+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "pig extensions cache: --max-size requires a byte size on prune")
				return 2
			}
			index++
			parsed, err := bytesize.Parse(args[index])
			if err != nil {
				fmt.Fprintln(os.Stderr, "pig extensions cache prune:", err)
				return 2
			}
			maxSize = &parsed
		case "-h", "--help":
			printExtensionsCacheHelp()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "pig extensions cache %s: unknown option %s\n", command, args[index])
			return 2
		}
	}

	cacheRoot := filepath.Join(codingagent.ConfigRoot(), "cache")
	current, resolutionErrors := currentExtensionCacheRoots(cacheRoot)
	if command == "prune" {
		if err := requireCompleteCurrentCacheRoots(resolutionErrors); err != nil {
			fmt.Fprintln(os.Stderr, "pig extensions cache:", err)
			return 1
		}
	}
	options := runtimecell.CacheLifecycleOptions{
		CacheRoot: cacheRoot, Current: current, LiveFingerprints: currentSDKFingerprints(),
		Retention: retention, MaxSize: maxSize, DryRun: dryRun, RemoveFailures: failures,
	}
	var report runtimecell.CacheReport
	var err error
	if command == "stats" {
		report, err = runtimecell.InspectCache(options)
	} else {
		report, err = runtimecell.PruneCaches(options)
	}
	for _, resolutionErr := range resolutionErrors {
		report.Errors = append(report.Errors, "current root: "+resolutionErr.Error())
	}
	if jsonOutput {
		data, encodeErr := json.Marshal(report)
		if encodeErr != nil {
			fmt.Fprintln(os.Stderr, "pig extensions cache: encode report:", encodeErr)
			return 1
		}
		fmt.Println(string(data))
	} else {
		fmt.Printf("Extension cache: %d entries, %d bytes\n", len(report.Entries), report.TotalBytes)
		if command == "prune" {
			fmt.Printf("Removed: %d entries, %d bytes\n", report.Removed, report.RemovedBytes)
			if report.Skipped > 0 {
				fmt.Printf("Skipped (in use): %d entries\n", report.Skipped)
			}
			if maxSize != nil && !report.LimitSatisfied {
				fmt.Printf("Limit satisfied: false (protected bytes: %d)\n", report.ProtectedBytes)
			}
		}
		for _, reportErr := range report.Errors {
			fmt.Fprintln(os.Stderr, "pig extensions cache:", reportErr)
		}
		if failure := readAutomaticCacheGCFailure(); failure != "" {
			fmt.Fprintln(os.Stderr, "pig extensions cache: the last automatic prune failed:", failure)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pig extensions cache:", err)
		return 1
	}
	if len(report.Errors) > 0 || maxSize != nil && !report.LimitSatisfied {
		return 1
	}
	return 0
}

func requireCompleteCurrentCacheRoots(resolutionErrors []error) error {
	if len(resolutionErrors) == 0 {
		return nil
	}
	messages := make([]string, len(resolutionErrors))
	for i, err := range resolutionErrors {
		messages[i] = err.Error()
	}
	return fmt.Errorf("current root resolution failed; cache was not pruned: %s", strings.Join(messages, "; "))
}

func currentExtensionCacheRoots(cacheRoot string) (map[string]struct{}, []error) {
	cwd, agentDir, settings, err := packageContext()
	if err != nil {
		return nil, []error{err}
	}
	configs := normalizeRuntimeExtensionConfigs(collectExtensionConfigs(cwd, agentDir, settings, Args{}, nil))
	return subprocess.CurrentCacheEntries(configs, cacheRoot)
}

func currentSDKFingerprints() map[string]struct{} {
	root := filepath.Join(codingagent.ConfigRoot(), "state", "pigsdk")
	fingerprints := make(map[string]struct{})
	for buildType, dir := range map[string]string{
		"go": filepath.Join(root, "sdk"), "rust": filepath.Join(root, "sdk-rs"),
		"python": filepath.Join(root, "sdk-py"),
	} {
		fingerprint, err := subprocess.StagedSDKFingerprint(dir, buildType)
		if err == nil && fingerprint != "" {
			fingerprints[fingerprint] = struct{}{}
		}
	}
	return fingerprints
}

// automaticCacheLimit bounds the extension cache the daily automatic prune leaves behind. Entries the current
// extensions use, and entries in use, are never removed to meet it, so the cache can exceed it.
// pig additive (D20): the packed-runtime cache has no upstream equivalent.
var automaticCacheLimit int64 = 5 << 30

// runAutomaticExtensionCacheGC collects the shared extension cache once a day. A collection that skipped
// entries that were in use still records its marker; a failed or cancelled one does not, and runs again at
// the next start. It runs in the background (startAutomaticExtensionCacheGC), never before pig accepts input.
func runAutomaticExtensionCacheGC(ctx context.Context, configs []subprocess.ExtConfig) error {
	cacheRoot := filepath.Join(codingagent.ConfigRoot(), "cache")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return err
	}
	marker := filepath.Join(cacheRoot, ".last-auto-gc")
	autoGCDue := func() bool {
		info, err := os.Stat(marker)
		// pig additive (D20): bound automatic cleanup of packed-runtime caches to once daily.
		return err != nil || automaticCacheGCNow().Sub(info.ModTime()) >= 24*time.Hour
	}
	if !autoGCDue() {
		return nil
	}
	// The collection never waits for the lock: a holder is already collecting.
	lock := flock.New(filepath.Join(cacheRoot, ".auto-gc.lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	if !autoGCDue() {
		return nil
	}
	current, resolutionErrors := subprocess.CurrentCacheEntries(configs, cacheRoot)
	if len(resolutionErrors) > 0 {
		return fmt.Errorf("resolve current extension cache roots: %v", resolutionErrors)
	}
	limit := automaticCacheLimit
	report, err := automaticCacheGCPrune(runtimecell.CacheLifecycleOptions{
		Context: ctx, CacheRoot: cacheRoot, Current: current, LiveFingerprints: currentSDKFingerprints(), MaxSize: &limit,
		Now: automaticCacheGCNow, Rename: automaticCacheGCRename, RemoveAll: automaticCacheGCRemoveAll,
	})
	if err != nil {
		return err
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("prune extension cache: %s", strings.Join(report.Errors, "; "))
	}
	temporary, err := os.CreateTemp(cacheRoot, ".last-auto-gc-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, marker); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func printExtensionsCacheHelp() {
	fmt.Print("Usage:\n  pig extensions cache stats [--json]\n  pig extensions cache prune [--retention <duration>] [--max-size <bytes>] [--failures] [--dry-run] [--json]\n\n--failures removes recorded build failures so the next start compiles those extensions again.\n")
}
