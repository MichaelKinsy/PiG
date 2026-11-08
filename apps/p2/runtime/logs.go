package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// Ports apps/p2/src/runtime/logs.ts: diagnostics. Redaction happens before a line enters a bounded source buffer.
//
// Forced Go differences. Node's `process.stdout.write` patch is a pipe in front of the real descriptor (CaptureProcessLogs),
// since Go's os.Stdout is a file; the pipe's reader tees. Test processes are recognised with testing.Testing() where
// upstream looks for NODE_TEST_CONTEXT. A file's fingerprint is its size and modification time (Node's adds the inode and
// change time, which Windows files lack). Maps by insertion order are slices of keys.

// LogLevel is a log line's stream or kind.
type LogLevel string

// The levels.
const (
	LogStdout LogLevel = "stdout"
	LogStderr LogLevel = "stderr"
	LogEvent  LogLevel = "event"
	LogError  LogLevel = "error"
)

// LogLine is one retained line.
type LogLine struct {
	ID    int      `json:"id"`
	Time  int64    `json:"time"`
	Level LogLevel `json:"level"`
	Text  string   `json:"text"`
}

// LogSource is a source's summary.
type LogSource struct {
	ID        string `json:"id"`
	Lines     int    `json:"lines"`
	UpdatedAt int64  `json:"updatedAt"`
}

// LogSnapshot is a source's published lines.
type LogSnapshot struct {
	Source string    `json:"source"`
	Lines  []LogLine `json:"lines"`
}

// LogNotice is an owner notice.
type LogNotice struct {
	ID      int    `json:"id"`
	Time    int64  `json:"time"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

const redacted = "[redacted]"

var (
	//portlint:allow regexfold extra folds (the Kelvin sign, long s) only redact more
	sensitiveKey = regexp.MustCompile(`(?i)(?:api[-_]?key|access[-_]?token|refresh[-_]?token|token|secret|password|authorization|cookie|credential|private[-_]?key|^key$|^access$|^refresh$)`)
	//portlint:allow regexfold extra folds (the Kelvin sign, long s) only redact more
	secretParam = regexp.MustCompile(`(?i)^(?:code|state|token|access_token|refresh_token|id_token|api_key)$`)
	//portlint:allow regexfold extra folds (the Kelvin sign, long s) only redact more
	headerRedact = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|cookie|set-cookie)\s*:\s*[^\r\n]*`)
	//portlint:allow regexfold extra folds (the Kelvin sign, long s) only redact more
	bearerRedact = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9+/=_~.%-]+`)
	//portlint:allow regexfold extra folds (the Kelvin sign, long s) only redact more
	urlUserRedact = regexp.MustCompile(`(?i)(https?://)[^\s/@]+:[^\s/@]+@`)
	skRedact      = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+\b`)
	//portlint:allow regexfold extra folds (the Kelvin sign, long s) only redact more
	fieldRedact = regexp.MustCompile(`(?i)(["']?(?:api[-_]?key|access[-_]?token|refresh[-_]?token|token|secret|password|cookie|authorization|proxy[-_]?secret|key|access|refresh)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&}]+)`)
	lineBreak   = regexp.MustCompile(`\r?\n`)
)

// redactedFiles are the files of a registered directory whose values are secrets.
var redactedFiles = []string{"auth.json", "radius.json", "models.json", "settings.json", "envs.json", "sandbox-secrets.json", "discord.json", "owner-key", "owner-token.txt"}

// LogRedactor holds literal secrets, which supplement structural redaction (a key printed without its field name).
type LogRedactor struct {
	mu           sync.Mutex
	secrets      map[string]struct{}
	directories  []string
	fingerprints map[string]string
	// seenEnv is the environment entries refresh has judged already, so a redaction does not match every entry's name again.
	seenEnv map[string]struct{}
	// ordered is every secret and its percent-encoded form, longest first; nil when a secret was added since it was built.
	ordered []string
}

// NewLogRedactor returns an empty redactor.
func NewLogRedactor() *LogRedactor {
	return &LogRedactor{secrets: map[string]struct{}{}, fingerprints: map[string]string{}, seenEnv: map[string]struct{}{}}
}

// AddSecret makes value (and the parts of a URL it is) a secret.
func (r *LogRedactor) AddSecret(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addSecret(value)
}

// insert records a secret, and drops the ordered list when it is new.
func (r *LogRedactor) insert(secret string) {
	if _, ok := r.secrets[secret]; !ok {
		r.secrets[secret] = struct{}{}
		r.ordered = nil
	}
}

func (r *LogRedactor) addSecret(value string) {
	if value == "" || value == redacted {
		return
	}
	r.insert(value)
	if trimmed := strings.TrimSpace(value); trimmed != "" && trimmed != redacted {
		r.insert(trimmed)
	}
	// Line capture must also protect multiline values (private credentials) line by line.
	for _, line := range lineBreak.Split(value, -1) {
		if line != "" && line != redacted {
			r.insert(line)
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return
	}
	if name := parsed.User.Username(); name != "" {
		r.insert(name)
	}
	if password, ok := parsed.User.Password(); ok && password != "" {
		r.insert(password)
	}
	fragment, _ := url.ParseQuery(parsed.Fragment)
	for _, parameters := range []url.Values{parsed.Query(), fragment} {
		for name, tokens := range parameters {
			if !secretParam.MatchString(name) {
				continue
			}
			for _, token := range tokens {
				if token != "" {
					r.insert(token)
				}
			}
		}
	}
}

// AddDirectory registers a directory whose credential files are read for secrets at each redaction.
func (r *LogRedactor) AddDirectory(directory string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.directories, directory) {
		r.directories = append(r.directories, directory)
	}
}

// RemoveDirectoriesUnder stops reading the credential files of the registered directories at root or below it (a server's data
// directory that is gone): every redaction stats each file of every registered directory, so a process that starts servers one
// after another (a test binary) would read the files of all it ever started. The secrets already read stay.
func (r *LogRedactor) RemoveDirectoriesUnder(root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := filepath.Clean(root) + string(filepath.Separator)
	r.directories = slices.DeleteFunc(r.directories, func(directory string) bool {
		clean := filepath.Clean(directory)
		return clean == filepath.Clean(root) || strings.HasPrefix(clean, prefix)
	})
	for path := range r.fingerprints {
		if strings.HasPrefix(path, prefix) {
			delete(r.fingerprints, path)
		}
	}
}

func (r *LogRedactor) collect(value any, sensitive bool) {
	switch typed := value.(type) {
	case string:
		if sensitive {
			r.addSecret(typed)
		}
	case []any:
		for _, entry := range typed {
			r.collect(entry, sensitive)
		}
	case map[string]any:
		for key, entry := range typed {
			r.collect(entry, sensitive || sensitiveKey.MatchString(key))
		}
	}
}

// refresh reads the environment's and the directories' secrets. Prior secrets stay through a partial write, a deletion,
// a logout or a configuration error.
func (r *LogRedactor) refresh() {
	for _, entry := range os.Environ() {
		if _, seen := r.seenEnv[entry]; seen {
			continue
		}
		r.seenEnv[entry] = struct{}{}
		key, value, _ := strings.Cut(entry, "=")
		if value != "" && sensitiveKey.MatchString(key) {
			r.addSecret(value)
		}
	}
	for _, directory := range r.directories {
		for _, name := range redactedFiles {
			path := filepath.Join(directory, name)
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			fingerprint := strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
			if r.fingerprints[path] == fingerprint {
				continue
			}
			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := string(content)
			if !strings.HasSuffix(name, ".json") {
				r.addSecret(strings.TrimSpace(text))
				r.fingerprints[path] = fingerprint
				continue
			}
			var parsed any
			if json.Unmarshal([]byte(strings.TrimPrefix(text, "\uFEFF")), &parsed) != nil {
				continue
			}
			// Sandbox secrets: `{ <sandbox>: { <NAME>: { value, hosts, ... } } }`; only the values are secret.
			if name == "sandbox-secrets.json" {
				if sandboxes, ok := parsed.(map[string]any); ok {
					for _, secrets := range sandboxes {
						named, _ := secrets.(map[string]any)
						for _, secret := range named {
							if fields, ok := secret.(map[string]any); ok {
								if value, ok := fields["value"].(string); ok {
									r.addSecret(value)
								}
							}
						}
					}
				}
			}
			r.collect(parsed, false)
			r.fingerprints[path] = fingerprint
		}
	}
}

// order is the secrets and their percent-encoded forms, longest first: overlapping secrets cannot leave an unredacted suffix.
// The result is never modified, so a redaction uses it outside the lock.
func (r *LogRedactor) order() []string {
	all := make([]string, 0, 2*len(r.secrets))
	for secret := range r.secrets {
		all = append(all, secret)
		if encoded := encodeURIComponent(secret); encoded != secret {
			all = append(all, encoded)
		}
	}
	slices.SortFunc(all, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return slices.Compact(all)
}

// Redact is text without its secrets.
func (r *LogRedactor) Redact(text string) string {
	r.mu.Lock()
	r.refresh()
	if r.ordered == nil {
		r.ordered = r.order()
	}
	secrets := r.ordered
	r.mu.Unlock()
	result := text
	for _, secret := range secrets {
		result = strings.ReplaceAll(result, secret, redacted)
	}
	result = headerRedact.ReplaceAllString(result, "${1}: "+redacted)
	result = bearerRedact.ReplaceAllString(result, "${1} "+redacted)
	result = urlUserRedact.ReplaceAllString(result, "${1}"+redacted+"@")
	result = skRedact.ReplaceAllString(result, redacted)
	return fieldRedact.ReplaceAllString(result, "${1}"+redacted)
}

// encodeURIComponent is JavaScript's: everything but the unreserved set and `!~*'()` is percent-encoded.
func encodeURIComponent(text string) string {
	const unreserved = "-_.!~*'()"
	var out strings.Builder
	for _, b := range []byte(text) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte(unreserved, b) >= 0 {
			out.WriteByte(b)
		} else {
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}

// LogLimits bound a store: lines and bytes per source, the number of sources and a line's bytes. Zero takes the default.
type LogLimits struct {
	Lines, Bytes, Sources, LineBytes int
}

type logBuffer struct {
	lines     []LogLine
	bytes     int
	updatedAt int64
}

// LogStore bounds the memory of the logs: per-source limits plus a source limit, abandoned workers included.
type LogStore struct {
	Redactor *LogRedactor

	mu        sync.Mutex
	order     []string
	buffers   map[string]*logBuffer
	listeners map[int]func(source string)
	nextID    int
	listener  int
	noticeID  int
	notices   []LogNotice
	limits    LogLimits
}

// NewLogStore returns a store with limits (zero fields default: 1000 lines and 256 KiB per source, 128 sources, 16 KiB
// per line).
func NewLogStore(limits LogLimits) (*LogStore, error) {
	for _, field := range []struct {
		value *int
		by    int
	}{{&limits.Lines, 1000}, {&limits.Bytes, 256 * 1024}, {&limits.Sources, 128}, {&limits.LineBytes, 16 * 1024}} {
		if *field.value == 0 {
			*field.value = field.by
		}
		if *field.value < 1 {
			return nil, errors.New("Invalid log bounds")
		}
	}
	return &LogStore{Redactor: NewLogRedactor(), buffers: map[string]*logBuffer{}, listeners: map[int]func(string){}, limits: limits}, nil
}

func nowMillis() int64 { return time.Now().UnixMilli() }

// Append redacts text, then retains its lines of source: the full input is redacted before it is truncated, so no prefix
// of a secret is kept.
func (s *LogStore) Append(source string, level LogLevel, text string) {
	s.AppendAt(source, level, text, nowMillis())
}

// AppendAt is Append at time (unix milliseconds).
func (s *LogStore) AppendAt(source string, level LogLevel, text string, time int64) {
	safe := s.Redactor.Redact(text)
	s.mu.Lock()
	for _, line := range lineBreak.Split(safe, -1) {
		entry := s.buffers[source]
		if entry == nil {
			if len(s.buffers) >= s.limits.Sources {
				delete(s.buffers, s.order[0])
				s.order = s.order[1:]
			}
			entry = &logBuffer{updatedAt: time}
		} else {
			s.order = slices.DeleteFunc(s.order, func(each string) bool { return each == source })
		}
		// Order is least recently appended first.
		s.order = append(s.order, source)
		s.buffers[source] = entry
		bounded := line
		if len(line) > s.limits.LineBytes {
			bounded = "[oversized log line omitted]"
		}
		s.nextID++
		entry.lines = append(entry.lines, LogLine{ID: s.nextID, Time: time, Level: level, Text: bounded})
		entry.bytes += len(bounded)
		entry.updatedAt = time
		for len(entry.lines) > s.limits.Lines || entry.bytes > s.limits.Bytes {
			entry.bytes -= len(entry.lines[0].Text)
			entry.lines = entry.lines[1:]
		}
	}
	listeners := s.listenersLocked()
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(source)
	}
}

func (s *LogStore) listenersLocked() []func(string) {
	ids := make([]int, 0, len(s.listeners))
	for id := range s.listeners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	listeners := make([]func(string), len(ids))
	for i, id := range ids {
		listeners[i] = s.listeners[id]
	}
	return listeners
}

// Subscribe calls listener with the source of each change (a line, a dismissed notice); it returns the unsubscribe.
func (s *LogStore) Subscribe(listener func(source string)) (unsubscribe func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener++
	id := s.listener
	s.listeners[id] = listener
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners, id)
	}
}

// Notice records an owner notice for source (replacing its earlier one; the last 50 are kept) and logs it as an error.
func (s *LogStore) Notice(source, message string) {
	safe := s.Redactor.Redact(message)
	bounded := safe
	if len(safe) > s.limits.LineBytes {
		bounded = "[oversized error message omitted]"
	}
	s.mu.Lock()
	s.noticeID++
	kept := slices.DeleteFunc(slices.Clone(s.notices), func(notice LogNotice) bool { return notice.Source == source })
	kept = append(kept, LogNotice{ID: s.noticeID, Time: nowMillis(), Source: source, Message: bounded})
	if len(kept) > 50 {
		kept = kept[len(kept)-50:]
	}
	s.notices = kept
	s.mu.Unlock()
	s.Append(source, LogError, bounded)
}

// Notices are the current owner notices.
func (s *LogStore) Notices() []LogNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.notices)
}

// DismissNotice removes notice id.
func (s *LogStore) DismissNotice(id int) {
	s.mu.Lock()
	var found *LogNotice
	for i := range s.notices {
		if s.notices[i].ID == id {
			notice := s.notices[i]
			found = &notice
		}
	}
	s.notices = slices.DeleteFunc(slices.Clone(s.notices), func(notice LogNotice) bool { return notice.ID == id })
	listeners := s.listenersLocked()
	s.mu.Unlock()
	if found != nil {
		for _, listener := range listeners {
			listener(found.Source)
		}
	}
}

// Lines are the retained lines of source (copies).
func (s *LogStore) Lines(source string) []LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.buffers[source]; entry != nil {
		return slices.Clone(entry.lines)
	}
	return []LogLine{}
}

// Sources are the retained sources, least recently appended first.
func (s *LogStore) Sources() []LogSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	sources := make([]LogSource, 0, len(s.order))
	for _, id := range s.order {
		entry := s.buffers[id]
		sources = append(sources, LogSource{ID: id, Lines: len(entry.lines), UpdatedAt: entry.updatedAt})
	}
	return sources
}

// ProcessLogs is this process's logs; its buffers belong to the process.
var ProcessLogs = func() *LogStore {
	store, err := NewLogStore(LogLimits{})
	if err != nil {
		panic(err)
	}
	return store
}()

// LogWriter writes chunks as complete UTF-8 lines of source (a secret split across writes is redacted as a whole).
type LogWriter struct {
	source  string
	level   LogLevel
	store   *LogStore
	forward func(line string)

	mu        sync.Mutex
	partial   []byte
	pending   string
	oversized bool
}

// NewLogWriter returns a writer of source at level into store (ProcessLogs when nil); forward gets each redacted line.
func NewLogWriter(source string, level LogLevel, store *LogStore, forward func(line string)) *LogWriter {
	if store == nil {
		store = ProcessLogs
	}
	return &LogWriter{source: source, level: level, store: store, forward: forward}
}

func (w *LogWriter) publish(text string) {
	safe := w.store.Redactor.Redact(text)
	w.store.Append(w.source, w.level, safe)
	if w.forward != nil {
		w.forward(safe)
	}
}

func (w *LogWriter) consume(text string) {
	for index, part := range strings.Split(text, "\n") {
		if index > 0 {
			if w.oversized {
				w.publish("[oversized log line omitted]")
			} else {
				w.publish(strings.TrimSuffix(w.pending, "\r"))
			}
			w.pending, w.oversized = "", false
		}
		if !w.oversized {
			w.pending += part
			if len(w.pending) > 64*1024 {
				w.pending, w.oversized = "", true
			}
		}
	}
}

// decode is the complete UTF-8 of the held bytes and chunk; an incomplete sequence at its end waits for the next chunk.
func (w *LogWriter) decode(chunk []byte) string {
	data := append(w.partial, chunk...)
	cut := len(data)
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		tail := data[len(data)-back:]
		if r, _ := utf8.DecodeRune(tail); r == utf8.RuneError && !utf8.FullRune(tail) && utf8.RuneStart(tail[0]) {
			cut = len(data) - back
			break
		}
	}
	w.partial = bytes.Clone(data[cut:])
	return string([]rune(string(data[:cut])))
}

// Write logs the complete lines of chunk.
func (w *LogWriter) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.consume(w.decode(chunk))
	return len(chunk), nil
}

// WriteString logs the complete lines of text.
func (w *LogWriter) WriteString(text string) {
	_, _ = w.Write([]byte(text))
}

// Discard drops an incomplete line after a capture overflow, then resumes at the next newline.
func (w *LogWriter) Discard() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial, w.pending, w.oversized = nil, "", true
}

// End publishes what is left.
func (w *LogWriter) End() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.consume(string([]rune(string(w.partial))))
	w.partial = nil
	if w.pending != "" || w.oversized {
		if w.oversized {
			w.publish("[oversized log line omitted]")
		} else {
			w.publish(w.pending)
		}
	}
	w.pending, w.oversized = "", false
}

var (
	capturesMu sync.Mutex
	captures   = map[string]*capture{}
)

type capture struct {
	references int
	stop       func()
}

// CaptureProcessLogs mirrors this process's stdout and stderr into ProcessLogs as source, without changing what they
// write; call the result when the owning host closes. Test processes use stdout as a reporter transport, so a test
// captures only when inTests says so.
func CaptureProcessLogs(source string, inTests bool) (release func()) {
	if !inTests && testing.Testing() {
		return func() {}
	}
	capturesMu.Lock()
	defer capturesMu.Unlock()
	existing := captures[source]
	if existing == nil {
		stops := []func(){teeStream(source, LogStdout, &os.Stdout), teeStream(source, LogStderr, &os.Stderr)}
		existing = &capture{stop: func() {
			for _, stop := range stops {
				stop()
			}
		}}
		captures[source] = existing
	}
	existing.references++
	var once sync.Once
	return func() {
		once.Do(func() {
			capturesMu.Lock()
			defer capturesMu.Unlock()
			if existing.references--; existing.references == 0 {
				existing.stop()
				delete(captures, source)
			}
		})
	}
}

// teeStream replaces *stream with a pipe whose reader writes to the original and, redacted, to the logs.
func teeStream(source string, level LogLevel, stream **os.File) (stop func()) {
	original := *stream
	reader, writer, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	logs := NewLogWriter(source, level, nil, nil)
	*stream = writer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 32*1024)
		for {
			n, err := reader.Read(buffer)
			if n > 0 {
				// The original write comes first and is never changed; the copy is optional.
				_, _ = original.Write(buffer[:n])
				_, _ = logs.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return func() {
		if *stream == writer {
			*stream = original
		}
		_ = writer.Close()
		<-done
		_ = reader.Close()
		logs.End()
	}
}

// PersistLogSource publishes source's bounded, already redacted snapshot to path atomically (a detached host's logs),
// shortly after each change; the result writes the last one and stops.
func PersistLogSource(source, path string, store *LogStore) (stop func() error) {
	if store == nil {
		store = ProcessLogs
	}
	var (
		mu      sync.Mutex
		timer   *time.Timer
		writing bool
		pending []byte
		wrote   = sync.NewCond(&mu)
	)
	var flush func()
	run := func() {
		for {
			mu.Lock()
			content := pending
			pending = nil
			if content == nil {
				writing = false
				wrote.Broadcast()
				mu.Unlock()
				return
			}
			mu.Unlock()
			// Diagnostics must not break their host.
			if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
				temporary := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
				if os.WriteFile(temporary, content, 0o600) == nil {
					_ = os.Rename(temporary, path)
				}
			}
		}
	}
	flush = func() {
		content, _ := json.Marshal(LogSnapshot{Source: source, Lines: store.Lines(source)})
		mu.Lock()
		defer mu.Unlock()
		pending = content
		// A slow disk keeps only the latest pending snapshot, never an unbounded queue.
		if writing {
			return
		}
		writing = true
		//portlint:allow golifetime the only writer goroutine: started only when none runs (writing) and ends when the queue drains
		go run()
	}
	unsubscribe := store.Subscribe(func(changed string) {
		mu.Lock()
		defer mu.Unlock()
		if changed != source || timer != nil {
			return
		}
		timer = time.AfterFunc(50*time.Millisecond, func() {
			mu.Lock()
			timer = nil
			mu.Unlock()
			flush()
		})
	})
	flush()
	return func() error {
		unsubscribe()
		mu.Lock()
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		mu.Unlock()
		flush()
		mu.Lock()
		defer mu.Unlock()
		for writing {
			wrote.Wait()
		}
		return nil
	}
}

var _ io.Writer = (*LogWriter)(nil)

// PublishedLogs is a detached host's bounded log publication (`capturePublishedLogs`): the terminal host or the dev
// supervisor publishes `<directory>/<source>.logs.json`, private and redacted.
type PublishedLogs struct {
	source, directory, path string
	stopCapture             func()
	persist                 func() error
	once                    sync.Once
}

// CapturePublishedLogs starts the publication of source ("terminals" or "dev") under directory. In tests it publishes
// nothing unless inTests (Pi's NODE_TEST_CONTEXT / `--test` check). Go forced difference: Node's
// `uncaughtExceptionMonitor` and `exit` hooks have no Go counterpart (a panic's own report is written by the runtime to the
// file descriptor, past os.Stderr), so the host reports its fatal error through Fatal, which writes the last snapshot
// synchronously, as Pi's exit hook does.
func CapturePublishedLogs(source, directory string, inTests bool) *PublishedLogs {
	if !inTests && testing.Testing() {
		return &PublishedLogs{}
	}
	ProcessLogs.Redactor.AddDirectory(directory)
	path := filepath.Join(directory, source+".logs.json")
	return &PublishedLogs{
		source: source, directory: directory, path: path,
		persist:     PersistLogSource(source, path, ProcessLogs),
		stopCapture: CaptureProcessLogs(source, inTests),
	}
}

// Fatal records a fatal error of the host (an error's text, or a panic's value with its stack) and writes the final
// snapshot at once: the host exits right after.
func (p *PublishedLogs) Fatal(value any, stack []byte) {
	if p == nil || p.persist == nil {
		return
	}
	text := fmt.Sprint(value)
	if err, ok := value.(error); ok {
		text = err.Error()
	}
	if len(stack) > 0 {
		text += "\n" + string(stack)
	}
	ProcessLogs.Append(p.source, LogError, text)
	p.once.Do(func() {
		p.stopCapture()
		// Diagnostics must not change process termination.
		if os.MkdirAll(p.directory, 0o700) != nil {
			return
		}
		content, _ := json.Marshal(LogSnapshot{Source: p.source, Lines: ProcessLogs.Lines(p.source)})
		temporary := fmt.Sprintf("%s.%d.exit.tmp", p.path, os.Getpid())
		if os.WriteFile(temporary, content, 0o600) == nil {
			_ = os.Rename(temporary, p.path)
		}
	})
}

// Stop ends the publication and writes the last snapshot.
func (p *PublishedLogs) Stop() error {
	if p == nil || p.persist == nil {
		return nil
	}
	var err error
	p.once.Do(func() {
		p.stopCapture()
		err = p.persist()
	})
	return err
}
