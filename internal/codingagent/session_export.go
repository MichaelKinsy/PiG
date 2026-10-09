package codingagent

// session_export.go ports upstream core/session-export.ts: the current branch
// serialized as a standalone JSONL session, with optional export-only entries
// appended after it (the pi.share presentation entry). The session-level HTML
// export of core/export-html/index.ts is in session_export_html.go.

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/tui"
)

// TrailingEntries builds export-only entries appended after the branch. It
// receives the last branch entry ID (nil for an empty branch) and the export
// timestamp. Mirrors upstream TrailingEntries.
type TrailingEntries func(parentID *string, timestamp string) []any

// marshalJSONLine encodes one JSONL record as JSON.stringify does: compact
// and without HTML escaping.
func marshalJSONLine(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// SerializeSessionBranch writes a fresh session header, the current branch
// with each entry's parentId chained to the previous entry, and any trailing
// entries, one JSON object per line. Mirrors upstream serializeSessionBranch.
func SerializeSessionBranch(header SessionHeader, branch []SessionEntry, now time.Time, createTrailingEntries TrailingEntries) (string, error) {
	type exportHeader struct {
		Type      string `json:"type"`
		Version   int    `json:"version"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		CWD       string `json:"cwd"`
	}
	timestamp := isoTimestamp(now)
	var b strings.Builder
	line, err := marshalJSONLine(exportHeader{Type: "session", Version: CurrentSessionVersion, ID: header.ID, Timestamp: timestamp, CWD: header.CWD})
	if err != nil {
		return "", err
	}
	b.Write(line)
	b.WriteByte('\n')
	var parentID *string
	for _, entry := range branch {
		rewritten, err := replaceJSONField(entry.Raw(), "parentId", parentID)
		if err != nil {
			return "", fmt.Errorf("entry %s: %w", entry.Base().ID, err)
		}
		b.Write(rewritten)
		b.WriteByte('\n')
		id := entry.Base().ID
		parentID = &id
	}
	if createTrailingEntries != nil {
		for _, trailing := range createTrailingEntries(parentID, timestamp) {
			line, err := marshalJSONLine(trailing)
			if err != nil {
				return "", err
			}
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// ExportSessionToJsonl writes the session's current branch (and optional
// trailing entries) to outputPath, resolved against the process working
// directory, or to session-<ISO timestamp>.jsonl there when outputPath is
// empty. It returns the resolved path. Mirrors upstream exportSessionToJsonl.
func ExportSessionToJsonl(session *Session, outputPath string, createTrailingEntries TrailingEntries) (string, error) {
	now := time.Now()
	if outputPath == "" {
		outputPath = "session-" + strings.NewReplacer(":", "-", ".", "-").Replace(isoTimestamp(now)) + ".jsonl"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	filePath, err := resolveExportPath(outputPath, cwd)
	if err != nil {
		return "", err
	}
	// session-export.ts:41-43 creates the directory only when it does not exist (existsSync follows links, so a file in its place fails at the write), and
	// its errors are Node's: "EACCES: permission denied, mkdir '<dir>'", "ENOTDIR: not a directory, open '<path>'".
	if dir := filepath.Dir(filePath); !pathExists(dir) {
		if err := nodeerrno.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	content, err := SerializeSessionBranch(session.GetHeader(), BugReportBranch(session), now, createTrailingEntries)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	return filePath, nil
}

// resolveExportPath mirrors upstream resolvePath(input, baseDir): expand a
// leading ~, accept a file:// URL, then resolve against baseDir.
func resolveExportPath(input, baseDir string) (string, error) {
	normalized := ExpandTildePath(input)
	if strings.HasPrefix(normalized, "file://") {
		if parsed, err := url.Parse(normalized); err == nil {
			normalized = parsed.Path
		}
	}
	return resolveAgainstCwd(normalized, baseDir)
}

// replaceJSONField sets field in a JSON object, keeping member order: an
// existing member is replaced in place and a missing one is appended, as a
// JavaScript object spread does. The result is compact.
func replaceJSONField(raw json.RawMessage, field string, value any) ([]byte, error) {
	return rewriteJSONField(raw, field, value, false)
}

func rewriteJSONField(raw json.RawMessage, field string, value any, remove bool) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("session entry is not a JSON object")
	}
	replacement, err := marshalJSONLine(value)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	replaced := false
	first := true
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyToken.(string)
		var member json.RawMessage
		if err := decoder.Decode(&member); err != nil {
			return nil, err
		}
		if key == field && remove {
			replaced = true
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		encodedKey, _ := marshalJSONLine(key)
		out.Write(encodedKey)
		out.WriteByte(':')
		if key == field {
			out.Write(replacement)
			replaced = true
			continue
		}
		if err := json.Compact(&out, member); err != nil {
			return nil, err
		}
	}
	if !replaced && !remove {
		if !first {
			out.WriteByte(',')
		}
		encodedKey, _ := marshalJSONLine(field)
		out.Write(encodedKey)
		out.WriteByte(':')
		out.Write(replacement)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// ExportThemeName is the theme AgentSession.exportToHtml exports with (agent-session.ts exportToHtml): the first candidate
// (the caller's themeName option, then the settings theme) that names a registered theme, or empty when none does.
func ExportThemeName(candidates ...string) string {
	for _, candidate := range candidates {
		if candidate != "" && tui.ThemeByName(candidate) != nil {
			return candidate
		}
	}
	return ""
}
