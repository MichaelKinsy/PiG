package runtimecell

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/gofrs/flock"
)

type toolchainVersionRecord struct {
	Fingerprint string `json:"fingerprint"`
	Output      string `json:"output"`
}

// commandVersion returns a cached version probe keyed by the resolved command,
// its file identity, and tool-selector state. Warm startups stat the selected
// tool and read one small record without spawning it.
func commandVersion(cacheRoot, name string, args ...string) string {
	cachePath := toolchainVersionCachePath(cacheRoot, name, args)
	if fingerprint, _, err := toolchainFingerprint(name, args); err == nil {
		if output, ok := readToolchainVersion(cachePath, fingerprint); ok {
			return output
		}
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "error:" + err.Error()
	}
	lock := flock.New(cachePath + ".lock")
	if err := lock.Lock(); err != nil {
		return "error:" + err.Error()
	}
	defer func() { _ = lock.Unlock() }()

	for range 2 {
		fingerprint, resolved, err := toolchainFingerprint(name, args)
		if err != nil {
			return "error:" + err.Error()
		}
		if output, ok := readToolchainVersion(cachePath, fingerprint); ok {
			return output
		}
		cmd := exec.Command(resolved, args...)
		out, err := cmd.CombinedOutput()
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
		return output
	}
	return "error:toolchain changed while its version was being probed"
}

func invalidateCommandVersion(cacheRoot, name string, args ...string) {
	_ = os.Remove(toolchainVersionCachePath(cacheRoot, name, args))
}

func toolchainVersionCachePath(cacheRoot, name string, args []string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(filepath.Base(name)))
	for _, arg := range args {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(arg))
	}
	return filepath.Join(cacheRoot, "toolchains", hex.EncodeToString(h.Sum(nil))+".json")
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
