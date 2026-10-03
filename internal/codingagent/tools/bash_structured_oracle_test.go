//go:build unix

package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// Replays testdata/bash-structured-oracle.json, recorded from the real upstream 0.99.1 bash tool
// (bash-structured-oracle.mjs, .upstream/v0.99.1/packages/coding-agent/src/core/tools/bash.ts:389-411),
// against BashTool byte for byte: model-facing text, structuredContent keys and output, details keys,
// including the 1 MiB head/tail cut on multi-byte characters.
func TestBashStructuredResultMatchesRecordedUpstream(t *testing.T) {
	data, err := os.ReadFile("testdata/bash-structured-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Upstream string `json:"upstream"`
		Cases    map[string]struct {
			Command                     string    `json:"command"`
			IsError                     bool      `json:"isError"`
			DetailsKeys                 *[]string `json:"detailsKeys"`
			TextSha256                  string    `json:"textSha256"`
			StructuredKeys              []string  `json:"structuredKeys"`
			Truncated                   bool      `json:"truncated"`
			ExitCode                    int       `json:"exitCode"`
			FullOutputPathIsDetailsPath bool      `json:"fullOutputPathIsDetailsPath"`
			OutputBytes                 int       `json:"outputBytes"`
			OutputSha256                string    `json:"outputSha256"`
			OutputHead                  string    `json:"outputHead"`
			OutputTail                  string    `json:"outputTail"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Upstream != "0.99.2" || len(oracle.Cases) != 6 {
		t.Fatalf("oracle = %s with %d cases", oracle.Upstream, len(oracle.Cases))
	}
	sha := func(text string) string { sum := sha256.Sum256([]byte(text)); return hex.EncodeToString(sum[:]) }
	units := func(text string, head bool) string {
		u := utf16.Encode([]rune(text))
		if len(u) > 16 {
			if head {
				u = u[:16]
			} else {
				u = u[len(u)-16:]
			}
		}
		return string(utf16.Decode(u))
	}
	for name, want := range oracle.Cases {
		t.Run(name, func(t *testing.T) {
			result := bashPort(t, &BashTool{CWD: t.TempDir()}, want.Command)
			var detailsKeys *[]string
			var detailsPath string
			if details, ok := result.Details.(*BashDetails); ok {
				keys := []string{"truncation", "fullOutputPath"}
				detailsKeys, detailsPath = &keys, details.FullOutputPath
				t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
			} else if result.Details != nil {
				t.Fatalf("details = %#v", result.Details)
			}
			if !slices.Equal(deref(detailsKeys), deref(want.DetailsKeys)) || (detailsKeys == nil) != (want.DetailsKeys == nil) {
				t.Errorf("details keys = %v, want %v", detailsKeys, want.DetailsKeys)
			}
			text := result.Text()
			if detailsPath != "" {
				text = strings.ReplaceAll(text, detailsPath, "<path>")
			}
			if result.IsError != want.IsError || sha(text) != want.TextSha256 {
				t.Errorf("isError=%v text differs from the recorded upstream text (%q...)", result.IsError, text[:min(len(text), 60)])
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(result.StructuredContent, &raw); err != nil {
				t.Fatal(err)
			}
			var keys []string
			dec := json.NewDecoder(strings.NewReader(string(result.StructuredContent)))
			_, _ = dec.Token()
			for dec.More() {
				key, _ := dec.Token()
				keys = append(keys, key.(string))
				var skip json.RawMessage
				_ = dec.Decode(&skip)
			}
			if !slices.Equal(keys, want.StructuredKeys) {
				t.Errorf("structuredContent keys = %v, want %v", keys, want.StructuredKeys)
			}
			var output string
			var truncated bool
			var exitCode int
			var fullPath string
			_ = json.Unmarshal(raw["output"], &output)
			_ = json.Unmarshal(raw["truncated"], &truncated)
			_ = json.Unmarshal(raw["exit_code"], &exitCode)
			_ = json.Unmarshal(raw["full_output_path"], &fullPath)
			if truncated != want.Truncated || exitCode != want.ExitCode || len(output) != want.OutputBytes || sha(output) != want.OutputSha256 {
				t.Errorf("truncated=%v exit=%d bytes=%d, want truncated=%v exit=%d bytes=%d (sha match %v)", truncated, exitCode, len(output), want.Truncated, want.ExitCode, want.OutputBytes, sha(output) == want.OutputSha256)
			}
			if units(output, true) != want.OutputHead || units(output, false) != want.OutputTail {
				t.Errorf("head/tail = %q/%q, want %q/%q", units(output, true), units(output, false), want.OutputHead, want.OutputTail)
			}
			if (fullPath == detailsPath) != want.FullOutputPathIsDetailsPath {
				t.Errorf("full_output_path = %q, details path %q", fullPath, detailsPath)
			}
		})
	}
}

func deref(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}
