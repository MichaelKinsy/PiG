//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// jsonModeLines decodes pig --mode json output, one object per line.
func jsonModeLines(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("stdout line is not JSON: %q: %v", scanner.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines
}

// toolExecutionEnd returns the tool_execution_end record for toolName.
func toolExecutionEnd(t *testing.T, lines []map[string]any, toolName string) map[string]any {
	t.Helper()
	for _, line := range lines {
		if line["type"] == "tool_execution_end" && line["toolName"] == toolName {
			return line
		}
	}
	t.Fatalf("no tool_execution_end for %s in %v", toolName, lines)
	return nil
}

// finalAssistantText returns the text of the last assistant message_end.
func finalAssistantText(lines []map[string]any) string {
	text := ""
	for _, line := range lines {
		if line["type"] != "message_end" {
			continue
		}
		message, _ := line["message"].(map[string]any)
		if message["role"] != "assistant" {
			continue
		}
		text = ""
		content, _ := message["content"].([]any)
		for _, block := range content {
			if block, ok := block.(map[string]any); ok && block["type"] == "text" {
				text += block["text"].(string)
			}
		}
	}
	return text
}

// TestJSONModeUsesPrintModeSessionOptions pins upstream print-mode.ts, where
// text and json output are one function over one session: --mode json must
// honor the same tool selection, attachments, and session name as -p. Pig's
// separate JSON runner dropped @image attachments, --exclude-tools, the
// default builtin tool set, and --name.
func TestJSONModeUsesPrintModeSessionOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	t.Run("exclude-tools removes the tool", func(t *testing.T) {
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, "--mode", "json", "-xt", "bash", "Run: expr 20 + 22")
		if code != 0 {
			t.Fatalf("exit %d, stderr %s", code, stderr)
		}
		if end := toolExecutionEnd(t, jsonModeLines(t, stdout), "bash"); end["isError"] != true {
			t.Fatalf("excluded bash ran: %v", end)
		}
	})

	t.Run("default builtin tool set leaves ls inactive", func(t *testing.T) {
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, "--mode", "json", "Run: ls here")
		if code != 0 {
			t.Fatalf("exit %d, stderr %s", code, stderr)
		}
		if end := toolExecutionEnd(t, jsonModeLines(t, stdout), "ls"); end["isError"] != true {
			t.Fatalf("ls ran although upstream's default tools are read, bash, edit, write: %v", end)
		}
	})

	t.Run("image attachments reach the provider", func(t *testing.T) {
		dir := t.TempDir()
		imagePath := filepath.Join(dir, "sample.png")
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(imagePath, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, "--mode", "json", "@"+imagePath, "describe sample image")
		if code != 0 {
			t.Fatalf("exit %d, stderr %s", code, stderr)
		}
		if got := finalAssistantText(jsonModeLines(t, stdout)); got != "cli-image-mime-ok" {
			t.Fatalf("final assistant text = %q, want cli-image-mime-ok (the faux provider saw the image)", got)
		}
	})

	t.Run("name is persisted", func(t *testing.T) {
		agentDir := t.TempDir()
		stdout, stderr, code := runPigForModeTestIn(t, bin, agentDir, devNull, nil, "--mode", "json", "--name", "json-named", "reply with exactly: named")
		if code != 0 {
			t.Fatalf("exit %d, stderr %s", code, stderr)
		}
		lines := jsonModeLines(t, stdout)
		if len(lines) == 0 || lines[0]["type"] != "session" {
			t.Fatalf("line 1 is not the session header: %v", lines)
		}
		sessionID, _ := lines[0]["id"].(string)
		name := sessionInfoName(t, agentDir, sessionID)
		if name != "json-named" {
			t.Fatalf("session %s name = %q, want json-named", sessionID, name)
		}
	})
}

// sessionInfoName returns the latest session_info name recorded in the session
// file for sessionID under agentDir.
func sessionInfoName(t *testing.T, agentDir, sessionID string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*"+sessionID+".jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session file for %s: %v %v", sessionID, files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for line := range strings.Lines(string(data)) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["type"] == "session_info" {
			name, _ = entry["name"].(string)
		}
	}
	return name
}
