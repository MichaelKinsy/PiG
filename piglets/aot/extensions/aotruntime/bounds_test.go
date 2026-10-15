package aotruntime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A checksum list or signature larger than its bound is refused before it is read whole: a mirror cannot make the install buffer an unbounded body.
func TestInstallRefusesAnOversizedChecksumList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, zeros{}, maxChecksumsSize+1)
	}))
	t.Cleanup(server.Close)
	in := installer{baseURL: server.URL, client: server.Client()}
	if _, err := in.fetch(context.Background(), checksumsFile, maxChecksumsSize); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("fetch = %v, want a size error", err)
	}
	if data, err := in.fetch(context.Background(), checksumsFile, maxChecksumsSize+1); err != nil || int64(len(data)) != maxChecksumsSize+1 {
		t.Fatalf("fetch at the bound = %d bytes, %v", len(data), err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// The build refuses a compiled host that starts but does not map its compiled image: it would run every function from bytecode.
func TestCheckImageRefusesAHostWithoutItsImage(t *testing.T) {
	dir := t.TempDir()
	for name, output := range map[string]string{"mapped": "image registered: true", "unmapped": "image registered: false"} {
		exe := filepath.Join(dir, name)
		if err := os.WriteFile(exe, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := checkImage(context.Background(), buildOptions{WorkDir: dir}, exe, io.Discard)
		if (err == nil) != (name == "mapped") {
			t.Errorf("%s: checkImage = %v", name, err)
		}
	}
}
