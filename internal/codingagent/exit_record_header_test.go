package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ownMarkerHeader writes this process's session marker and returns the header it carries.
func ownMarkerHeader(t *testing.T) sessionMarkerHeader {
	t.Helper()
	agentDir := t.TempDir()
	BeginSessionMarker(agentDir, "9.9.9", "/work")
	t.Cleanup(EndSessionMarker)
	data, err := os.ReadFile(filepath.Join(CrashOutputDir(agentDir), fmt.Sprintf("%d.log", os.Getpid())))
	if err != nil {
		t.Fatal(err)
	}
	headerLine, _, _ := strings.Cut(string(data), "\n")
	var header sessionMarkerHeader
	if err := json.Unmarshal([]byte(headerLine), &header); err != nil {
		t.Fatal(err)
	}
	return header
}

// The marker this process writes names the host it runs on, which is what the other hosts compare.
func TestSessionMarkerHeaderNamesTheHost(t *testing.T) {
	header := ownMarkerHeader(t)
	hostname, _ := os.Hostname()
	if header.Host != hostname || header.Host == "" {
		t.Fatalf("header host = %q, want %q", header.Host, hostname)
	}
}
