package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestReadPinRequiresVersionAndCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstream.go")
	if err := os.WriteFile(path, []byte("const UpstreamVersion = \"0.84.0\"\nconst UpstreamCommit = \"abc\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	version, commit, err := readPin(path)
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.84.0" || commit != "abc" {
		t.Fatalf("pin = %q %q", version, commit)
	}
}

func TestHashFilesIncludesNamesAndBytesDeterministically(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a.ts")
	second := filepath.Join(root, "b.ts")
	for path, content := range map[string]string{first: "same", second: "same"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	forward, err := hashFiles(root, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := hashFiles(root, []string{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if forward != reverse {
		t.Fatalf("hash depends on caller order: %s != %s", forward, reverse)
	}
	if err := os.Rename(second, filepath.Join(root, "c.ts")); err != nil {
		t.Fatal(err)
	}
	renamed, err := hashFiles(root, []string{first, filepath.Join(root, "c.ts")})
	if err != nil {
		t.Fatal(err)
	}
	if renamed == forward {
		t.Fatal("hash ignored source path")
	}
}

func TestCorpusEnvironmentIsolatesHomesAndProxies(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid")
	t.Setenv("HOME", "/wrong")
	home := t.TempDir()
	environment := corpusEnvironment(home)
	for _, forbidden := range []string{"HTTPS_PROXY=http://proxy.invalid", "HOME=/wrong"} {
		if slices.Contains(environment, forbidden) {
			t.Fatalf("environment retained %q", forbidden)
		}
	}
	for _, required := range []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig")} {
		if !slices.Contains(environment, required) {
			t.Fatalf("environment missing %q", required)
		}
	}
}

func TestValidateExampleRecordsFailedRegistration(t *testing.T) {
	root := t.TempDir()
	corpus := filepath.Join(root, "corpus")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(corpus, "example.ts")
	if err := os.WriteFile(source, []byte("export default () => {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "pig")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	copyTestBinary(t, binary)
	t.Setenv(fakePigOutputEnv, `{"valid":true,"registered":false,"name":"example","code":"not_registered"}`)
	got, err := validateExample(root, binary, corpus, source, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.Registered || got.Code != "not_registered" || got.Source != "example.ts" {
		t.Fatalf("result = %+v", got)
	}
	if len(got.SourceSHA256) != 64 {
		t.Fatalf("source hash = %q", got.SourceSHA256)
	}
}
