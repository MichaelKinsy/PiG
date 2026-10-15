package codingagent

// session_export.go ports upstream core/session-export.ts: the current branch
// serialized as a standalone JSONL session, with optional export-only entries
// appended after it (the pi.share presentation entry). The session-level HTML
// export of core/export-html/index.ts is in session_export_html.go.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
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
	// encoding/json writes U+2028 and U+2029 as escapes; JSON.stringify writes them as themselves.
	return jsonstringify.Canonicalize(bytes.TrimSuffix(buffer.Bytes(), []byte("\n")))
}

// ExportHeader is the header serializeSessionBranch writes: the session's own id with the manager's resolved cwd (sessionManager.getCwd()), which differs from the
// stored header's cwd when that was spelled relative, with a `..` segment, or without a drive on Windows.
// upstream: packages/coding-agent/src/core/session-export.ts:serializeSessionBranch
func (s *Session) ExportHeader() SessionHeader {
	header := s.GetHeader()
	header.CWD = s.CWD()
	return header
}

// SerializeSessionBranch writes a fresh session header, the current branch
// with each entry's parentId chained to the previous entry, and any trailing
// entries, one JSON object per line. Mirrors upstream serializeSessionBranch.
func SerializeSessionBranch(header SessionHeader, branch []SessionEntry, now time.Time, createTrailingEntries TrailingEntries) (string, error) {
	lines, size, err := sessionBranchLines(header, branch, now, createTrailingEntries)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(size)
	for _, line := range lines {
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// sessionBranchLines is SerializeSessionBranch's lines, without their newlines, and the bytes they take with them.
func sessionBranchLines(header SessionHeader, branch []SessionEntry, now time.Time, createTrailingEntries TrailingEntries) (lines [][]byte, size int, err error) {
	type exportHeader struct {
		Type      string `json:"type"`
		Version   int    `json:"version"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		CWD       string `json:"cwd"`
	}
	timestamp := isoTimestamp(now)
	line, err := marshalJSONLine(exportHeader{Type: "session", Version: CurrentSessionVersion, ID: header.ID, Timestamp: timestamp, CWD: header.CWD})
	if err != nil {
		return nil, 0, err
	}
	lines = make([][]byte, 0, len(branch)+1)
	lines = append(lines, line)
	var parentID *string
	for _, entry := range branch {
		rewritten, err := replaceJSONField(entry.Raw(), "parentId", parentID)
		if err != nil {
			return nil, 0, fmt.Errorf("entry %s: %w", entry.Base().ID, err)
		}
		lines = append(lines, rewritten)
		id := entry.Base().ID
		parentID = &id
	}
	if createTrailingEntries != nil {
		for _, trailing := range createTrailingEntries(parentID, timestamp) {
			line, err := marshalJSONLine(trailing)
			if err != nil {
				return nil, 0, err
			}
			lines = append(lines, line)
		}
	}
	for _, line := range lines {
		size += len(line) + 1
	}
	return lines, size, nil
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
	cwd, err := nodepath.Cwd() // session-export.ts:39 process.cwd()
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
	// Every line is made before the file is opened, as session-export.ts builds the content before writeFileSync.
	lines, _, err := sessionBranchLines(session.ExportHeader(), BugReportBranch(session), now, createTrailingEntries)
	if err != nil {
		return "", err
	}
	if err := writeLines(filePath, lines); err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	return filePath, nil
}

// writeLines writes lines, each followed by a newline, to path as os.WriteFile writes a file, through one buffer.
func writeLines(path string, lines [][]byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, sessionWriteBufferFor(lines))
	for _, line := range lines {
		_, _ = w.Write(line)
		_ = w.WriteByte('\n')
	}
	err = w.Flush()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// resolveExportPath mirrors upstream resolvePath(input, baseDir): expand a
// leading ~, accept a file:// URL through fileURLToPath, then resolve against
// baseDir.
func resolveExportPath(input, baseDir string) (string, error) {
	return ResolvePath(input, baseDir)
}

// replaceJSONField sets field in a JSON object, keeping member order: an
// existing member is replaced in place and a missing one is appended, as a
// JavaScript object spread does. The result is compact.
func replaceJSONField(raw json.RawMessage, field string, value any) ([]byte, error) {
	return rewriteJSONField(raw, field, value, false)
}

func rewriteJSONField(raw json.RawMessage, field string, value any, remove bool) ([]byte, error) {
	if !remove {
		if out, ok, err := spliceCanonicalMember(raw, field, value); ok || err != nil {
			return out, err
		}
	}
	out, err := rewriteMemberJSONField(raw, field, value, remove)
	if err != nil {
		return nil, err
	}
	// A JavaScript object is the parsed record: duplicate members collapse, integer-like names come first, numbers and escapes print as JSON.stringify prints them.
	return jsonstringify.Canonicalize(out)
}

// spliceCanonicalMember is replaceJSONField of a record that is JSON.stringify's text and names field once among its members: the value's
// encoding takes the place of the member's, which is what the member-by-member rewrite and Canonicalize write for such a record (a canonical
// object has no repeated or integer-like names, so the members keep their order and their text). ok is false for any other record.
func spliceCanonicalMember(raw json.RawMessage, field string, value any) (out []byte, ok bool, err error) {
	start, end := canonicalMemberValue(raw, field)
	if start < 0 || !jsonstringify.IsCanonical(raw) {
		return nil, false, nil
	}
	replacement, err := marshalJSONLine(value)
	if err != nil {
		return nil, false, err
	}
	out = make([]byte, 0, len(raw)-(end-start)+len(replacement))
	out = append(append(append(out, raw[:start]...), replacement...), raw[end:]...)
	return out, true, nil
}

// canonicalMemberValue returns the bounds of the value of the top-level member named field in raw, a compact JSON object, when field
// names exactly one member with no escape in its name; -1 otherwise.
func canonicalMemberValue(raw []byte, field string) (start, end int) {
	if len(raw) < 2 || raw[0] != '{' {
		return -1, -1
	}
	start, end = -1, -1
	i := 1
	for i < len(raw) && raw[i] != '}' {
		if raw[i] != '"' {
			return -1, -1
		}
		quote := skipJSONStringBody(raw, i+1)
		if quote < 0 || quote+1 >= len(raw) || raw[quote+1] != ':' {
			return -1, -1
		}
		name := raw[i+1 : quote]
		valueEnd := skipJSONValue(raw, quote+2)
		if valueEnd < 0 {
			return -1, -1
		}
		if string(name) == field {
			if start >= 0 {
				return -1, -1
			}
			start, end = quote+2, valueEnd
		}
		i = valueEnd
		if i < len(raw) && raw[i] == ',' {
			i++
		}
	}
	if i != len(raw)-1 {
		return -1, -1
	}
	return start, end
}

func rewriteMemberJSONField(raw json.RawMessage, field string, value any, remove bool) ([]byte, error) {
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
