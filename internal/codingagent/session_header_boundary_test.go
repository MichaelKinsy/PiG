// SPDX-License-Identifier: MIT

package codingagent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRecentSessionDiscoveryUsesBoundedHeaderReader(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	want := fileOperationWrite(t, dir, "valid.jsonl", fileOperationHeader(cwd, "valid")+strings.Repeat("x", 2*maxSessionHeaderScanBytes))
	old := listSessionsInDir
	listSessionsInDir = func(string) ([]SessionInfo, error) {
		t.Fatal("discovery scanned transcript summaries")
		return nil, nil
	}
	defer func() { listSessionsInDir = old }()
	manager := NewSessionManagerWithDir(cwd, dir)
	if got := manager.FindMostRecentForContinue(); got != want {
		t.Fatal(got)
	}
	if got := manager.FindByID("valid"); got != want {
		t.Fatal(got)
	}
}

func TestSessionHeaderScanLimitAndExplicitOpenFallback(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	prefix := `{"type":"session","id":"`
	suffix := fmt.Sprintf(`","version":3,"timestamp":"2025-01-01T00:00:00Z","cwd":%q}`, cwd)
	content := prefix + strings.Repeat("x", maxSessionHeaderScanBytes-len(prefix)-len(suffix)) + suffix
	exact := fileOperationWrite(t, dir, "exact.jsonl", content)
	if header, err := ReadSessionHeader(exact); err != nil || header == nil {
		t.Fatalf("exact limit header=%v err=%v", header, err)
	}
	large := fileOperationWrite(t, dir, "large.jsonl", content+" ")
	_, err := ReadSessionHeader(large)
	if _, ok := errors.AsType[*SessionHeaderScanLimitError](err); !ok {
		t.Fatalf("oversized header=%v", err)
	}
	if readSessionHeaderForDiscovery(large) != nil {
		t.Fatal("discovery accepted oversized header")
	}
	session, err := NewSessionManagerWithDir(cwd, dir).Open(large)
	if err != nil || session.CWD() != cwd {
		t.Fatalf("explicit open=%v err=%v", session, err)
	}
}
