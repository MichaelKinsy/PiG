package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
)

func shellPort(t *testing.T, name string, ops BashOperations) agent.AgentToolResult {
	t.Helper()
	cfg := shellToolConfig{name: name, shellName: name, tempFilePrefix: "pi-" + name, operations: ops}
	return runShell(t, t.Context(), t.TempDir(), cfg, bashParams{Command: "fake"})
}

func exitWith(code int, output string) BashOperations {
	return portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
		if output != "" {
			opts.OnData([]byte(output))
		}
		return BashOperationsResult{ExitCode: &code}, nil
	})
}

// bash.ts:389-411 (v0.99.1): bash and powershell share createShellToolDefinition, so a
// non-zero exit is a returned error result carrying structuredContent.
func TestShellToolStructuredResultForBothShells(t *testing.T) {
	for _, name := range []string{"bash", "powershell"} {
		t.Run(name, func(t *testing.T) {
			failed := shellPort(t, name, exitWith(2, "boom\n"))
			if !failed.IsError || failed.Text() != "boom\n\n\nCommand exited with code 2" {
				t.Fatalf("failed = %+v", failed)
			}
			if failed.Details != nil {
				t.Fatalf("details = %#v, want none (upstream returns details undefined)", failed.Details)
			}
			fields := structuredFields(t, failed)
			if fields["output"] != "boom\n" || fields["exit_code"] != float64(2) || fields["truncated"] != false || len(fields) != 4 {
				t.Fatalf("structuredContent = %v", fields)
			}
			ok := shellPort(t, name, exitWith(0, ""))
			if ok.IsError || ok.Text() != "(no output)" {
				t.Fatalf("ok = %+v", ok)
			}
			if fields := structuredFields(t, ok); fields["output"] != "" || fields["exit_code"] != float64(0) {
				t.Fatalf("ok structuredContent = %v", fields)
			}
		})
	}
}

// bash.ts:378-392: a missing exit code, abort and timeout still throw, so their results carry no structuredContent.
func TestShellToolThrownFailuresHaveNoStructuredContent(t *testing.T) {
	nullExit := portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
		opts.OnData([]byte("partial\n"))
		return BashOperationsResult{}, nil
	})
	for name, ops := range map[string]BashOperations{
		"null exit code": nullExit,
		"aborted": portBashOperations(func(context.Context, string, string, BashOperationsExecOptions) (BashOperationsResult, error) {
			return BashOperationsResult{}, errors.New("aborted")
		}),
		"timeout": portBashOperations(func(context.Context, string, string, BashOperationsExecOptions) (BashOperationsResult, error) {
			return BashOperationsResult{}, errors.New("timeout:5")
		}),
	} {
		t.Run(name, func(t *testing.T) {
			result := shellPort(t, "bash", ops)
			if !result.IsError || result.StructuredContent != nil {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

// bash.ts:394-407: a failing command whose output exceeds the display limits keeps the truncation
// details and the notice, and structuredContent omits full_output_path unless its own output is cut.
func TestShellToolNonZeroExitKeepsTruncationDetails(t *testing.T) {
	var output strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&output, "%d\n", i)
	}
	result := shellPort(t, "bash", exitWith(1, output.String()))
	details, ok := result.Details.(*BashDetails)
	if !ok || details.Truncation == nil || !details.Truncation.Truncated || details.FullOutputPath == "" {
		t.Fatalf("details = %#v", result.Details)
	}
	t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
	if !result.IsError || !strings.HasSuffix(result.Text(), "Full output: "+details.FullOutputPath+"]\n\nCommand exited with code 1") {
		t.Fatalf("text = %q", result.Text())
	}
	fields := structuredFields(t, result)
	if fields["output"] != output.String() || fields["truncated"] != false {
		t.Fatalf("truncated = %v", fields["truncated"])
	}
	if _, present := fields["full_output_path"]; present {
		t.Fatal("full_output_path present although the structured output is complete")
	}
}

// bash.ts:392: wall_time_seconds is Math.round(ms / 100) / 10.
func TestShellToolWallTimeRoundsToTenthsOfASecond(t *testing.T) {
	slow := portBashOperations(func(ctx context.Context, _, _ string, _ BashOperationsExecOptions) (BashOperationsResult, error) {
		time.Sleep(120 * time.Millisecond)
		return BashOperationsResult{ExitCode: new(0)}, nil
	})
	wall, _ := structuredFields(t, shellPort(t, "bash", slow))["wall_time_seconds"].(float64)
	if wall < 0.1 || math.Abs(wall*10-math.Round(wall*10)) > 1e-9 {
		t.Fatalf("wall_time_seconds = %v, want a multiple of 0.1 no smaller than 0.1", wall)
	}
}

// bash.ts:23-66 (v0.99.1) bashOutputSchema, also used by powershell. The expected bytes are
// JSON.stringify(createBashTool(cwd).outputSchema) and the same for createPowerShellTool, recorded from
// the real npm @earendil-works/pi-coding-agent@1.0.0 (TypeBox 1.3.27 puts required before properties). Pi 1.0.0 shortened
// the descriptions (.upstream/v1.0.0/packages/coding-agent/src/core/tools/bash.ts:56-62); PowerShell shares the schema.
func TestShellToolOutputSchema(t *testing.T) {
	const upstream = `{"type":"object","required":["output","truncated","exit_code","wall_time_seconds"],"properties":{"output":{"type":"string","description":"Combined stdout and stderr, possibly truncated"},"truncated":{"type":"boolean"},"full_output_path":{"type":"string","description":"Full output, when truncated"},"exit_code":{"type":"number"},"wall_time_seconds":{"type":"number"}}}`
	for name, provider := range map[string]interface{ OutputSchema() json.RawMessage }{"bash": &BashTool{}, "powershell": &PowerShellTool{}} {
		t.Run(name, func(t *testing.T) {
			var compact bytes.Buffer
			if err := json.Compact(&compact, provider.OutputSchema()); err != nil {
				t.Fatalf("OutputSchema %q: %v", provider.OutputSchema(), err)
			}
			if compact.String() != upstream {
				t.Fatalf("schema = %s\nwant %s", compact.String(), upstream)
			}
		})
	}
}

// shell.ts:153-161 (v0.99.1) sanitizeBinaryOutput removes exactly [\x00-\x08\x0B\x0C\x0E-\x1F\uFFF9-\uFFFB]
// (upstream's regex; the 0.87.1 filter removed the same code points, and Go strings hold no lone surrogates).
func TestSanitizeBinaryOutputRemovesExactlyTheUpstreamClass(t *testing.T) {
	for r := rune(0); r <= 0xFFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		removed := r <= 0x08 || r == 0x0B || r == 0x0C || (r >= 0x0E && r <= 0x1F) || (r >= 0xFFF9 && r <= 0xFFFB)
		got := SanitizeBinaryOutput("a" + string(r) + "b")
		want := "a" + string(r) + "b"
		if removed {
			want = "ab"
		}
		if got != want {
			t.Fatalf("U+%04X: got %q, want %q", r, got, want)
		}
	}
	if got := SanitizeBinaryOutput("x😀\x00y\U0001FFF9"); got != "x😀y\U0001FFF9" {
		t.Fatalf("astral characters: %q", got)
	}
}

// output-accumulator.ts:151-183 (v0.99.1) readFullOutput.
func TestReadFullOutput(t *testing.T) {
	t.Run("keeps everything in memory when no temp file exists", func(t *testing.T) {
		acc := NewOutputAccumulator("pi-test")
		_ = acc.Append([]byte("\xef\xbb\xbfhello "))
		_ = acc.Append([]byte("w\xf0\x9f"))
		_ = acc.Append([]byte("\x98\x80rld\xff\n"))
		acc.Finish()
		got, err := acc.ReadFullOutput(1024)
		if err != nil || got.Truncated || got.Content != "hello w😀rld�\n" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("empty output", func(t *testing.T) {
		acc := NewOutputAccumulator("pi-test")
		acc.Finish()
		got, err := acc.ReadFullOutput(1024)
		if err != nil || got.Truncated || got.Content != "" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("returns a file of at most maxBytes whole", func(t *testing.T) {
		text := "é0123456789abcdefgh" // 20 bytes
		for _, max := range []int{20, 21} {
			got, err := readFullAccumulator(t, text, 2000, 10).ReadFullOutput(max)
			if err != nil || got.Truncated || got.Content != text {
				t.Fatalf("max %d: got %+v, %v", max, got, err)
			}
		}
	})
	t.Run("keeps the first and last half around the omission marker", func(t *testing.T) {
		text := strings.Repeat("0123456789", 10) // 100 bytes
		acc := readFullAccumulator(t, text, 2000, 10)
		got, err := acc.ReadFullOutput(40)
		want := text[:20] + "\n\n[... 60 bytes omitted ...]\n\n" + text[80:]
		if err != nil || !got.Truncated || got.Content != want {
			t.Fatalf("got %+v, %v\nwant %q", got, err, want)
		}
	})
	t.Run("cuts multi-byte characters only at character boundaries", func(t *testing.T) {
		text := strings.Repeat("é", 20000) + "end" // 40003 bytes; é is c3 a9
		for _, tc := range []struct {
			max              int
			headRunes        int // runes kept from the start: the head holds back an incomplete trailing sequence
			tailStartsAtByte int // first byte of the tail once leading continuation bytes are skipped
		}{
			{1000, 250, 39504}, // tail starts on a continuation byte (offset 39503)
			{1002, 250, 39502}, // head ends inside a character (501 bytes)
			{1001, 250, 39502},
		} {
			acc := readFullAccumulator(t, text, 2000, 10)
			got, err := acc.ReadFullOutput(tc.max)
			if err != nil || !got.Truncated || strings.ContainsRune(got.Content, utf8.RuneError) || !strings.HasSuffix(got.Content, "end") {
				t.Fatalf("max %d: got truncated=%v err=%v content=%.40q", tc.max, got.Truncated, err, got.Content)
			}
			headBytes := tc.max / 2
			tailBytes := tc.max - headBytes
			omitted := len(text) - headBytes - tailBytes
			want := strings.Repeat("é", tc.headRunes) + fmt.Sprintf("\n\n[... %d bytes omitted ...]\n\n", omitted) + text[tc.tailStartsAtByte:]
			if got.Content != want {
				t.Fatalf("max %d: content differs (len %d, want %d)", tc.max, len(got.Content), len(want))
			}
		}
	})
	// Every branch decodes with a fresh default TextDecoder (ignoreBOM: false), which drops one leading U+FEFF
	// only; for the cut output the head and the tail each start a new decoder. Expected values are
	// readFullOutput results from the real npm @earendil-works/pi-coding-agent@0.99.1 OutputAccumulator
	// (maxBytes 10 forces the temp file).
	t.Run("drops a leading byte order mark in each decoded part", func(t *testing.T) {
		bom := "\xef\xbb\xbf"
		for _, tc := range []struct {
			name, text string
			max        int
			want       FullOutput
		}{
			{"whole file", bom + strings.Repeat("x", 100), 1024, FullOutput{Content: strings.Repeat("x", 100)}},
			{"head and tail", bom + strings.Repeat("a", 57) + bom + strings.Repeat("b", 17), 40, FullOutput{Content: strings.Repeat("a", 17) + "\n\n[... 40 bytes omitted ...]\n\n" + strings.Repeat("b", 17), Truncated: true}},
			{"inner mark kept", strings.Repeat("a", 10) + bom + strings.Repeat("b", 10), 1024, FullOutput{Content: strings.Repeat("a", 10) + "\uFEFF" + strings.Repeat("b", 10)}},
		} {
			got, err := readFullAccumulator(t, tc.text, 2000, 10).ReadFullOutput(tc.max)
			if err != nil || got != tc.want {
				t.Fatalf("%s: got %+v, %v; want %+v", tc.name, got, err, tc.want)
			}
		}
	})
	t.Run("reports a missing temp file", func(t *testing.T) {
		acc := readFullAccumulator(t, strings.Repeat("x", 100), 2000, 10)
		snapshot := acc.Snapshot(true)
		if err := os.Remove(snapshot.FullOutputPath); err != nil {
			t.Fatal(err)
		}
		if _, err := acc.ReadFullOutput(1024); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want not exist", err)
		}
	})
}

// readFullAccumulator feeds text through an accumulator with the given display limits (a small maxBytes
// forces the temp file), finishes it and closes the file, as bash.ts does before readFullOutput.
func readFullAccumulator(t *testing.T, text string, maxLines, maxBytes int) *OutputAccumulator {
	t.Helper()
	acc := newOutputAccumulator(maxLines, maxBytes, "pi-test")
	_ = acc.Append([]byte(text))
	acc.Finish()
	snapshot := acc.Snapshot(true)
	if err := acc.CloseTempFile(); err != nil {
		t.Fatal(err)
	}
	if snapshot.FullOutputPath != "" {
		t.Cleanup(func() { _ = os.Remove(snapshot.FullOutputPath) })
	}
	return acc
}
