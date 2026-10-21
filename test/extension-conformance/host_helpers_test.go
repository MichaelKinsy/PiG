package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestConformance_HostHelpersAgreeAcrossSDKs: Pi exports generateDiffString, generateUnifiedPatch, sessionEntryToContextMessages, convertToPng, resizeImage,
// readStoredCredential and copyToClipboard (coding-agent index.ts). Every SDK answers the first six as Pi's own code does at the pin, for the golden file's inputs
// (test/parity/sdk-vectors/pi-ai/*.json and pi-coding-agent/*.json): the diffs and patches byte for byte, the messages an entry contributes, the MIME type and
// dimensions of a converted or resized image (Pi's Photon and the host's encoder do not produce the same bytes), and the credential an auth.json holds in the key order
// of the file. copyToClipboard reaches the host's copy function with the exact text in the Go, Rust and Python SDKs; the Node runtime runs Pi's own copy code in its own
// process, which writes the system clipboard and never reaches the host, so the row does not call it there.
func TestConformance_HostHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	// Not parallel: it owns conformanceHostInfo, the process environment and the host's copy function while it runs.
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	conformanceHostInfo = subprocess.HostInfo{AgentDir: agentDir, ConfigDirName: ".pig", Version: coding.UpstreamPackageVersion}
	t.Cleanup(func() { conformanceHostInfo = subprocess.HostInfo{} })
	var copyMu sync.Mutex
	var copied []string
	subprocess.SetClipboardCopier(func(text string) error {
		copyMu.Lock()
		defer copyMu.Unlock()
		copied = append(copied, text)
		return nil
	})
	t.Cleanup(func() { subprocess.SetClipboardCopier(nil) })

	golden := piVectorsFile(t)
	content, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Diffs []struct {
			Diff             *string `json:"diff"`
			FirstChangedLine *int    `json:"firstChangedLine"`
			Patch            string  `json:"patch"`
		} `json:"diffs"`
		Entries []struct {
			Messages json.RawMessage `json:"messages"`
		} `json:"entries"`
		Images []json.RawMessage `json:"images"`
	}
	if err := json.Unmarshal(content, &want); err != nil {
		t.Fatal(err)
	}
	var wantImages []struct {
		Result json.RawMessage `json:"result"`
	}
	var imageCases struct {
		Images []struct {
			Result json.RawMessage `json:"result"`
		} `json:"images"`
	}
	if err := json.Unmarshal(content, &imageCases); err != nil {
		t.Fatal(err)
	}
	wantImages = imageCases.Images

	// An auth.json the SDK is pointed at, one at the default place (with a byte order mark), one that is not JSON, and one missing.
	const explicitCredential = `{"type":"api_key","key":"explicit-key","env":{"B":"2","A":"1"},"10":"x","2":"y","nested":{"z":1,"1":2,"é":"😀"},"list":[3,{"b":1,"a":2}]}`
	explicit := filepath.Join(t.TempDir(), "auth.json")
	mustWrite := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(explicit, `{"anthropic":`+explicitCredential+`,"other":5}`)
	mustWrite(explicit+".bad", "{not json")
	const defaultCredential = `{"type":"oauth","refresh":"r","access":"default-token","expires":1500}`
	mustWrite(filepath.Join(agentDir, "auth.json"), "\ufeff"+`{"anthropic":`+defaultCredential+`}`)
	const clipboardText = "copied \U0001F600 \u2028 text\n"

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "fused-go" {
				// The fused extension runs in this process, so the host must put the entry in this process's environment before the factory runs.
				t.Setenv("PIG_EXT_HOST_INFO", "")
			}
			copyMu.Lock()
			copied = nil
			copyMu.Unlock()
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			before := len(*h.notify)
			if !h.runner.ExecuteCommand(context.Background(), "host_helpers_probe", golden+"|"+explicit+"|"+clipboardText) {
				t.Fatal("fixture has no host_helpers_probe command")
			}
			var reported string
			pollUntilConformance(t, "host_helpers_probe: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "host_helpers:"); ok {
						reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			var got struct {
				Diffs []struct {
					Diff             *string `json:"diff"`
					FirstChangedLine *int    `json:"firstChangedLine"`
					Patch            string  `json:"patch"`
				} `json:"diffs"`
				Entries []json.RawMessage `json:"entries"`
				Images  []json.RawMessage `json:"images"`
				Creds   map[string]json.RawMessage
			}
			if err := json.Unmarshal([]byte(reported), &got); err != nil {
				t.Fatalf("report is not JSON: %v", err)
			}
			if len(got.Diffs) != len(want.Diffs) || len(got.Entries) != len(want.Entries) || len(got.Images) != len(wantImages) {
				t.Fatalf("answers %d/%d/%d for %d/%d/%d calls", len(got.Diffs), len(got.Entries), len(got.Images), len(want.Diffs), len(want.Entries), len(wantImages))
			}
			failures := 0
			fail := func(format string, args ...any) {
				failures++
				if failures <= 6 {
					t.Errorf(format, args...)
				}
			}
			for i, w := range want.Diffs {
				g := got.Diffs[i]
				if g.Patch != w.Patch {
					fail("diff case %d: patch %q, want %q", i, g.Patch, w.Patch)
				}
				if (g.Diff == nil) != (w.Diff == nil) || (g.Diff != nil && *g.Diff != *w.Diff) || (w.Diff != nil && (g.FirstChangedLine == nil) != (w.FirstChangedLine == nil)) || (g.FirstChangedLine != nil && *g.FirstChangedLine != *w.FirstChangedLine) {
					fail("diff case %d: diff %v at %v, want %v at %v", i, g.Diff, g.FirstChangedLine, w.Diff, w.FirstChangedLine)
				}
			}
			for i, w := range want.Entries {
				if g, e := piAiCanonicalJSON(t, got.Entries[i], false), piAiCanonicalJSON(t, w.Messages, false); g != e {
					fail("entry case %d: %s, want %s", i, g, e)
				}
			}
			for i, w := range wantImages {
				if g, e := piAiCanonicalJSON(t, got.Images[i], false), piAiCanonicalJSON(t, w.Result, false); g != e {
					fail("image case %d: %s, want %s", i, g, e)
				}
			}
			for name, wantText := range map[string]string{"explicit": explicitCredential, "default": defaultCredential, "absentProvider": "null", "missingFile": "null", "notJSON": "null"} {
				if g, e := piAiCanonicalJSON(t, got.Creds[name], true), piAiCanonicalJSON(t, []byte(wantText), true); g != e {
					fail("readStoredCredential %s: %s, want %s", name, g, e)
				}
			}
			copyMu.Lock()
			defer copyMu.Unlock()
			if strings.Contains(tc.name, "node") {
				if len(copied) != 0 {
					t.Errorf("a Node extension reached the host's copy function with %q", copied)
				}
			} else if len(copied) != 1 || copied[0] != clipboardText {
				t.Errorf("host copy function received %q, want exactly %q", copied, clipboardText)
			}
		})
	}
}
