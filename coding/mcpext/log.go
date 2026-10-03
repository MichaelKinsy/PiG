package mcpext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/coding-agent/src/extensions/mcp/log.ts.
//
// Log messages MCP servers send with `notifications/message`, appended to
// `mcp.log` in the agent directory. Several processes may write to the same
// file, so every message is one append. The file is rotated to `mcp.log.1`
// once it grows past maxLogBytes.

// upstream: packages/coding-agent/src/extensions/mcp/log.ts:MAX_LOG_BYTES
const maxLogBytes = 5 * 1024 * 1024

func formatLogData(data json.RawMessage) string {
	if len(data) == 0 {
		return "undefined"
	}
	if data[0] == '"' {
		var s string
		if json.Unmarshal(data, &s) == nil {
			return s
		}
	}
	// JSON.stringify(data): numbers and escapes are normalized.
	canonical, err := jsonstringify.Canonicalize(data)
	if err != nil {
		return string(data)
	}
	return string(canonical)
}

var lineBreak = lazyregexp.New(`\r?\n`)

// FormatMcpLogMessage formats one `notifications/message` from server as a log
// line; continuation lines are indented.
func FormatMcpLogMessage(server string, params json.RawMessage, now time.Time) string {
	message := orderedjson.New()
	if isJSONObject(params) {
		if parsed, err := orderedjson.Parse(params); err == nil {
			message = parsed
		}
	} else if params != nil {
		message.Set("data", params)
	}
	level := "info"
	if raw, ok := message.Get("level"); ok && len(raw) > 0 && raw[0] == '"' {
		_ = json.Unmarshal(raw, &level)
	}
	logger := ""
	if raw, ok := message.Get("logger"); ok && len(raw) > 0 && raw[0] == '"' {
		var name string
		if json.Unmarshal(raw, &name) == nil && name != "" {
			logger = " " + name + ":"
		}
	}
	data, _ := message.Get("data")
	text := lineBreak.ReplaceAllString(formatLogData(data), "\n    ")
	return now.UTC().Format("2006-01-02T15:04:05.000Z") + " [" + server + "] " + level + logger + " " + text + "\n"
}

// McpServerLog appends server log messages to one file. Write errors are
// ignored: logging must not break tools.
type McpServerLog struct {
	// Path is the log file.
	Path string
	mu   sync.Mutex
	size *int64
}

// NewMcpServerLog returns a log that writes to path.
func NewMcpServerLog(path string) *McpServerLog { return &McpServerLog{Path: path} }

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// Write appends the message of one `notifications/message` from server.
func (l *McpServerLog) Write(server string, params json.RawMessage) {
	line := FormatMcpLogMessage(server, params, time.Now())
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size == nil {
		if err := os.MkdirAll(filepath.Dir(l.Path), 0o777); err != nil {
			return
		}
		size := fileSize(l.Path)
		l.size = &size
	}
	if *l.size > maxLogBytes {
		// Another process may have rotated it already; check before renaming.
		if fileSize(l.Path) > maxLogBytes {
			if err := os.Rename(l.Path, l.Path+".1"); err != nil {
				return
			}
		}
		size := fileSize(l.Path)
		l.size = &size
	}
	file, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(line); err != nil {
		return
	}
	*l.size += int64(len(line))
}
