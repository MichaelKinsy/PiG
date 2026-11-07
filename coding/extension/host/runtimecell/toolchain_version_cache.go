package runtimecell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

type toolchainVersionRecord struct {
	Fingerprint string `json:"fingerprint"`
	Output      string `json:"output"`
}

// Bounds on a version probe. A toolchain manager can take minutes to answer (rustup downloading the toolchain a rust-toolchain.toml names), so the probe is bounded rather than left to hang a start, and a start that waits for a peer's probe of the same record waits no longer than toolchainLockWait before it probes for itself.
var (
	toolchainProbeTimeout = 2 * time.Minute // pig additive (D20): the shared cell cache and its version probes have no upstream equivalent
	toolchainLockWait     = 5 * time.Minute // pig additive (D20): see toolchainProbeTimeout
)

// toolchainProbeKillDelay is how long a killed probe may keep its output pipes open before Wait gives up on them: a rustup proxy leaves its download running in a child that holds them.
// pig additive (D20): see toolchainProbeTimeout.
const toolchainProbeKillDelay = 5 * time.Second

// toolchainSlotsPerCommand is how many fingerprints' records a command keeps; the least recently written go first.
const toolchainSlotsPerCommand = 8

// commandVersion returns a cached version probe keyed by the resolved command,
// its file identity, and tool-selector state. Warm startups stat the selected
// tool and read one small record without spawning it. Each fingerprint has its
// own record and lock, so two projects whose selectors differ (go.mod,
// rust-toolchain.toml) neither replace each other's record nor wait for each
// other's probe.
func commandVersion(cacheRoot, name string, args ...string) string {
	fingerprint, _, err := toolchainFingerprint(name, args)
	if err != nil {
		return "error:" + err.Error()
	}
	cachePath := toolchainVersionCachePath(cacheRoot, name, args, fingerprint)
	if output, ok := readToolchainVersion(cachePath, fingerprint); ok {
		return output
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "error:" + err.Error()
	}
	// The lock only keeps peers from probing the same record at once. A peer that holds it past toolchainLockWait is not waited for: this start probes for itself, and the record is published by rename, so the two cannot corrupt it.
	lock := flock.New(cachePath + ".lock")
	//portlint:allow ctxbackground commandVersion has no caller context: it runs under hashing a cell's inputs, and toolchainLockWait bounds the wait
	lockCtx, cancelLock := context.WithTimeout(context.Background(), toolchainLockWait)
	locked, err := lock.TryLockContext(lockCtx, 25*time.Millisecond) // pig additive (D20): poll interval of the bounded wait
	cancelLock()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return "error:" + err.Error()
	}
	if locked {
		defer func() { _ = lock.Unlock() }()
	}

	for range 2 {
		fingerprint, resolved, err := toolchainFingerprint(name, args)
		if err != nil {
			return "error:" + err.Error()
		}
		cachePath := toolchainVersionCachePath(cacheRoot, name, args, fingerprint)
		if output, ok := readToolchainVersion(cachePath, fingerprint); ok {
			return output
		}
		//perf:ok once per probe of an uncached toolchain, which spawns a process
		probeCtx, cancelProbe := context.WithTimeout(context.Background(), toolchainProbeTimeout) //portlint:allow ctxbackground commandVersion has no caller context: it runs under hashing a cell's inputs, and toolchainProbeTimeout bounds the probe
		cmd := linkerexec.CommandContext(probeCtx, resolved, args...)
		cmd.WaitDelay = toolchainProbeKillDelay
		out, err := cmd.CombinedOutput()
		timedOut := probeCtx.Err() != nil
		cancelProbe()
		if timedOut {
			return "error:" + filepath.Base(name) + " did not answer within " + toolchainProbeTimeout.String()
		}
		if err != nil {
			return "error:" + err.Error()
		}
		after, _, err := toolchainFingerprint(name, args)
		if err != nil {
			return "error:" + err.Error()
		}
		if after != fingerprint {
			continue
		}
		output := strings.TrimSpace(string(out))
		if err := writeToolchainVersion(cachePath, toolchainVersionRecord{Fingerprint: fingerprint, Output: output}); err != nil {
			return "error:" + err.Error()
		}
		pruneToolchainVersions(cacheRoot, name, args, cachePath)
		return output
	}
	return "error:toolchain changed while its version was being probed"
}

// invalidateCommandVersion forgets every record of the command, whatever fingerprint it was probed under.
func invalidateCommandVersion(cacheRoot, name string, args ...string) {
	for _, path := range toolchainVersionSlots(cacheRoot, name, args) {
		_ = os.Remove(path)
	}
}

// toolchainVersionPrefix is the file name prefix shared by one command's records.
func toolchainVersionPrefix(name string, args []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(filepath.Base(name)))
	for _, arg := range args {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(arg))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func toolchainVersionCachePath(cacheRoot, name string, args []string, fingerprint string) string {
	return filepath.Join(cacheRoot, "toolchains", toolchainVersionPrefix(name, args)+"-"+fingerprint+".json")
}

// toolchainVersionSlots lists the command's record files.
func toolchainVersionSlots(cacheRoot, name string, args []string) []string {
	slots, _ := filepath.Glob(filepath.Join(cacheRoot, "toolchains", toolchainVersionPrefix(name, args)+"-*.json"))
	return slots
}

// pruneToolchainVersions removes the records, and their locks, of the fingerprints written longest ago once the command has more than toolchainSlotsPerCommand of them. The record just written is kept.
func pruneToolchainVersions(cacheRoot, name string, args []string, keep string) {
	slots := toolchainVersionSlots(cacheRoot, name, args)
	if len(slots) <= toolchainSlotsPerCommand {
		return
	}
	modTime := make(map[string]time.Time, len(slots))
	for _, slot := range slots {
		if info, err := os.Stat(slot); err == nil { //perf:ok only when a command is over its record limit, after an uncached probe
			modTime[slot] = info.ModTime()
		}
	}
	slices.SortFunc(slots, func(a, b string) int { return modTime[b].Compare(modTime[a]) })
	kept := 0
	for _, slot := range slots {
		if slot == keep || kept < toolchainSlotsPerCommand {
			kept++
			continue
		}
		_ = os.Remove(slot)
		_ = os.Remove(slot + ".lock")
	}
}

func toolchainFingerprint(name string, args []string) (string, string, error) {
	selector := filepath.Base(name)
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", "", err
	}
	if physical, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = physical
	}
	revision, ok := toolchainFileRevision(resolved)
	if !ok {
		return "", "", fmt.Errorf("stat toolchain %s", resolved)
	}
	h := sha256.New()
	for _, value := range append([]string{resolved, revision}, args...) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	for _, value := range toolchainSelectorState(selector) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), resolved, nil
}

func toolchainSelectorState(name string) []string {
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	keys := []string{"PATH"}
	switch name {
	case "go":
		keys = append(keys, "GOTOOLCHAIN", "GOROOT", "GOENV", "GOPATH", "GOMODCACHE", "GOWORK")
	case "rustc", "cargo", "rustup":
		keys = append(keys, "RUSTUP_TOOLCHAIN", "RUSTUP_HOME", "CARGO_HOME")
	case "python", "python3", "py":
		keys = append(keys, "PYTHONHOME", "PYTHONPATH", "PYENV_ROOT", "PYENV_VERSION")
	}
	state := make([]string, 0, len(keys)+16)
	for _, key := range keys {
		state = append(state, key+"="+os.Getenv(key))
	}
	cwd, err := os.Getwd()
	if err == nil {
		var selectors []string
		switch name {
		case "go":
			selectors = []string{"go.work", "go.mod"}
		case "rustc", "cargo", "rustup":
			selectors = []string{"rust-toolchain.toml", "rust-toolchain"}
		case "python", "python3", "py":
			selectors = []string{".python-version", ".tool-versions", "mise.toml", ".mise.toml"}
		}
		state = append(state, nearestSelectorRevisions(cwd, selectors)...)
	}
	if name == "go" {
		state = append(state, goSelectorRevisions()...)
	}
	if name == "rustc" || name == "cargo" || name == "rustup" {
		state = append(state, rustupSelectorRevisions(name)...)
	}
	slices.Sort(state)
	return state
}

// nearestSelectorRevisions searches each selector name independently. Tool
// managers compose: a child go.mod does not hide a parent go.work, and one
// Python manager's child selector does not hide another manager's parent file.
func nearestSelectorRevisions(cwd string, names []string) []string {
	revisions := make([]string, 0, len(names))
	for _, name := range names {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			path := filepath.Join(dir, name)
			if revision, ok := toolchainFileRevision(path); ok {
				revisions = append(revisions, path+"="+revision)
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return revisions
}

func goSelectorRevisions() []string {
	var paths []string
	if work := strings.TrimSpace(os.Getenv("GOWORK")); work != "" && work != "off" && work != "auto" {
		paths = append(paths, work)
	}
	if goenv := strings.TrimSpace(os.Getenv("GOENV")); goenv != "off" {
		if goenv != "" {
			paths = append(paths, goenv)
		} else if configDir, err := os.UserConfigDir(); err == nil {
			paths = append(paths, filepath.Join(configDir, "go", "env"))
		}
	}
	modCache := strings.TrimSpace(os.Getenv("GOMODCACHE"))
	if modCache == "" {
		goPath := strings.TrimSpace(os.Getenv("GOPATH"))
		if goPath == "" {
			if home, err := os.UserHomeDir(); err == nil {
				goPath = filepath.Join(home, "go")
			}
		}
		if goPath != "" {
			modCache = filepath.Join(filepath.SplitList(goPath)[0], "pkg", "mod")
		}
	}
	if modCache != "" {
		matches, _ := filepath.Glob(filepath.Join(modCache, "golang.org", "toolchain@*", "bin", exeNameForRuntime("go")))
		paths = append(paths, matches...)
	}
	return pathRevisions(paths)
}

func rustupSelectorRevisions(name string) []string {
	root := strings.TrimSpace(os.Getenv("RUSTUP_HOME"))
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, ".rustup")
		}
	}
	if root == "" {
		return nil
	}
	paths := []string{filepath.Join(root, "settings.toml")}
	matches, _ := filepath.Glob(filepath.Join(root, "toolchains", "*", "bin", exeNameForRuntime(name)))
	paths = append(paths, matches...)
	return pathRevisions(paths)
}

func pathRevisions(paths []string) []string {
	slices.Sort(paths)
	revisions := make([]string, 0, len(paths))
	for _, path := range paths {
		if revision, ok := toolchainFileRevision(path); ok {
			revisions = append(revisions, path+"="+revision)
		}
	}
	return revisions
}

func exeNameForRuntime(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func readToolchainVersion(path, fingerprint string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record toolchainVersionRecord
	if decoder.Decode(&record) != nil || record.Fingerprint != fingerprint || record.Output == "" {
		return "", false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", false
	}
	return record.Output, true
}

func writeToolchainVersion(path string, record toolchainVersionRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".toolchain-version-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
