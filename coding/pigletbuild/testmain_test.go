package pigletbuild

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeGHStateEnv makes this test binary act as the GitHub CLI. The value is
// the per-test directory that records invocations and uploaded assets.
const fakeGHStateEnv = "PIG_TEST_FAKE_GH_STATE"

var (
	fakeGHOnce sync.Once
	fakeGHBin  string
	fakeGHErr  error
)

func TestMain(m *testing.M) {
	if os.Getenv(scriptEchoEnv) != "" {
		os.Exit(echoWorkingDirectoryAndArgs())
	}
	if state := os.Getenv(fakeGHStateEnv); state != "" {
		os.Exit(runFakeGH(state, os.Args[1:]))
	}
	code := m.Run()
	if fakeGHBin != "" {
		_ = os.RemoveAll(fakeGHBin)
	}
	os.Exit(code)
}

// fakeGHCall is one recorded gh invocation.
type fakeGHCall struct {
	Args           []string `json:"args"`
	PromptDisabled string   `json:"promptDisabled"`
}

// runFakeGH implements the gh release view/create subset publish uses. A
// state/exists file makes view find the release; state/fail-create makes
// create fail after recording the call.
func runFakeGH(state string, args []string) int {
	if err := appendFakeGHCall(state, args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fake gh:", err)
		return 98
	}
	switch {
	case len(args) >= 2 && args[0] == "release" && args[1] == "view":
		if _, err := os.Stat(filepath.Join(state, "exists")); err == nil {
			_, _ = fmt.Println(`{"tagName":"` + args[2] + `"}`)
			return 0
		}
		_, _ = fmt.Fprintln(os.Stderr, "release not found")
		return 1
	case len(args) >= 2 && args[0] == "release" && args[1] == "create":
		if _, err := os.Stat(filepath.Join(state, "fail-create")); err == nil {
			_, _ = fmt.Fprintln(os.Stderr, "HTTP 422: Validation Failed")
			return 1
		}
		for _, asset := range fakeGHCreateAssets(args[3:]) {
			if err := copyFakeGHAsset(asset, filepath.Join(state, "assets", filepath.Base(asset))); err != nil {
				_, _ = fmt.Fprintln(os.Stderr, "fake gh upload:", err)
				return 1
			}
		}
		_, _ = fmt.Println("https://github.com/fake/release")
		return 0
	}
	_, _ = fmt.Fprintf(os.Stderr, "fake gh: unexpected command %q\n", args)
	return 97
}

func appendFakeGHCall(state string, args []string) error {
	line, err := json.Marshal(fakeGHCall{Args: args, PromptDisabled: os.Getenv("GH_PROMPT_DISABLED")})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(state, "calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// fakeGHCreateAssets returns the positional asset arguments after the tag.
// Every flag publish passes to gh release create except --prerelease takes
// one value.
func fakeGHCreateAssets(args []string) []string {
	var assets []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--prerelease" {
			continue
		}
		if strings.HasPrefix(args[i], "--") {
			i++
			continue
		}
		assets = append(assets, args[i])
	}
	return assets
}

func copyFakeGHAsset(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

// fakeGH is one test's view of the fake GitHub CLI.
type fakeGH struct {
	t     *testing.T
	state string
}

// installFakeGH puts a gh executable that is this test binary first on PATH
// for the rest of the test.
func installFakeGH(t *testing.T) fakeGH {
	t.Helper()
	fakeGHOnce.Do(func() {
		fakeGHBin, fakeGHErr = os.MkdirTemp("", "pig-fake-gh-*")
		if fakeGHErr != nil {
			return
		}
		name := "gh"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		var self string
		if self, fakeGHErr = os.Executable(); fakeGHErr != nil {
			return
		}
		fakeGHErr = copyFakeGHAsset(self, filepath.Join(fakeGHBin, name))
	})
	if fakeGHErr != nil {
		t.Fatalf("install fake gh: %v", fakeGHErr)
	}
	state := t.TempDir()
	t.Setenv("PATH", fakeGHBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeGHStateEnv, state)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	return fakeGH{t: t, state: state}
}

func (g fakeGH) setExists() {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.state, "exists"), nil, 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g fakeGH) setFailCreate() {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.state, "fail-create"), nil, 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g fakeGH) calls() []fakeGHCall {
	g.t.Helper()
	data, err := os.ReadFile(filepath.Join(g.state, "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		g.t.Fatal(err)
	}
	var calls []fakeGHCall
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var call fakeGHCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			g.t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

func (g fakeGH) assets() map[string][]byte {
	g.t.Helper()
	entries, err := os.ReadDir(filepath.Join(g.state, "assets"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		g.t.Fatal(err)
	}
	assets := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(g.state, "assets", entry.Name()))
		if err != nil {
			g.t.Fatal(err)
		}
		assets[entry.Name()] = data
	}
	return assets
}
