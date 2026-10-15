// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsonvalid"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/procpolicy"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// FileEntry is one record of a session file: the header (SessionHeader) or an entry (SessionEntry) (session-manager.ts FileEntry).
type FileEntry = sessionentry.FileEntry

// ParseSessionEntries splits JSONL content into its records, in order. Ports packages/coding-agent/src/core/session-manager.ts (parseSessionEntries): the content is trimmed and split on "\n", a line that is not valid JSON, including a blank one, is skipped (only JSON whitespace may surround a record, so a line led by U+00A0 is skipped). Unlike [LoadEntriesFromFile] it keeps a record whose JSON value is falsy (null, false, 0, ""), because Pi pushes whatever JSON.parse returns.
func ParseSessionEntries(content string) []FileEntry {
	entries := []FileEntry{}
	//portlint:allow pathseparators Pi splits on "\n" alone (session-manager.ts parseSessionEntries: content.trim().split("\n")); a CRLF record keeps its \r and the JSON parse accepts it
	for line := range strings.SplitSeq(widthx.JSTrim(content), "\n") {
		if jsonvalid.Valid([]byte(line)) {
			entries = append(entries, sessionentry.DecodeFileEntry(json.RawMessage(strings.Trim(line, " \t\r\n"))))
		}
	}
	return entries
}

// Ports packages/coding-agent/src/core/session-manager.ts (loadEntriesFromFile).
// LoadEntriesFromFile skips malformed and JSON-falsy lines and requires a session header before accepting records.
func LoadEntriesFromFile(path string) ([]FileEntry, error) {
	records, unterminated, err := readSessionFileEntries(path)
	if err != nil {
		return nil, err
	}
	if unterminated {
		if _, err := appendSessionLine(path, nil); err != nil {
			return nil, err
		}
	}
	return records, nil
}

type sessionTailReader struct {
	io.Reader
	last byte
	read bool
}

func (r *sessionTailReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	if n > 0 {
		r.last = buffer[n-1]
		r.read = true
	}
	return n, err
}

// isObjectLine reports whether a line's first byte after JSON whitespace opens an object: a valid such line is a truthy record.
func isObjectLine(line []byte) bool {
	line = bytes.TrimLeft(line, " \t\r\n")
	return len(line) > 0 && line[0] == '{'
}

func truthySessionLine(line []byte) bool {
	// JSON.parse accepts only JSON whitespace around a record, so a line led by U+00A0 or U+0085 is skipped.
	line = bytes.Trim(line, " \t\r\n")
	if !jsonvalid.Valid(line) {
		return false
	}
	switch line[0] {
	case '{', '[', 't':
		return true
	case 'n', 'f':
		return false
	case '"':
		var value string
		if json.Unmarshal(line, &value) != nil {
			return false
		}
		return value != ""
	default:
		number, err := strconv.ParseFloat(string(line), 64)
		return (err == nil || errors.Is(err, strconv.ErrRange)) && number != 0
	}
}

func readSessionFileEntries(path string) ([]FileEntry, bool, error) {
	// loadEntriesFromFile returns no entries unless existsSync(path), which is false for any stat failure (a missing
	// file, or a parent that is a file); a path that exists but cannot be opened fails.
	if _, err := os.Stat(path); err != nil {
		return []FileEntry{}, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, nodeerrno.FromPathError(err)
	}
	defer func() { _ = file.Close() }()
	reader := &sessionTailReader{Reader: file}
	records := []FileEntry{}
	err = forEachJSONLLine(reader, func(line []byte) error {
		// readFileSync(path, "utf8") replaces each maximal invalid subpart with one U+FFFD before the lines are split.
		if !utf8.Valid(line) {
			line = []byte(jsstring.FromUTF8(line))
		}
		if truthySessionLine(line) {
			records = append(records, sessionentry.DecodeFileEntry(bytes.Clone(line)))
		}
		return nil
	})
	if err != nil {
		// readSync's error: "EISDIR: illegal operation on a directory, read".
		if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
			err = nodeerrno.FromPathError(pathErr)
		}
		return nil, false, err
	}
	if len(records) == 0 {
		return records, false, nil
	}
	if _, valid := records[0].(SessionHeader); !valid {
		return []FileEntry{}, false, nil
	}
	return records, reader.read && reader.last != '\n', nil
}

// EnsureSessionDir creates a persisted session's directory as Pi's SessionManager constructor does: it skips any path
// that exists, even a file, and otherwise creates it recursively with Node's error text
// (session-manager.ts:1011-1013: existsSync, then mkdirSync).
func EnsureSessionDir(dir string) error {
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	return nodeerrno.MkdirAll(dir, 0o755)
}

// Open opens an explicit session file, with an optional effective working-directory override. Like Pi's
// SessionManager.open, it first creates the manager's session directory: the caller passes the explicit session
// directory, else the file's parent (session-manager.ts:1784-1785).
func (sm *SessionManager) Open(path string, cwdOverride ...string) (*Session, error) {
	resolved, err := ResolvePath(path, "")
	if err != nil {
		return nil, err
	}
	if err := EnsureSessionDir(sm.sessionDir); err != nil {
		return nil, err
	}
	session, empty, err := loadPersistedSession(resolved)
	if err != nil {
		return nil, err
	}
	if empty {
		// _setSessionFile: a path existsSync rejects, for any stat failure, starts a new Session there.
		info, statErr := os.Stat(resolved)
		if statErr == nil && info.Size() > 0 {
			return nil, fmt.Errorf("Session file is not a valid pi session: %s", resolved)
		}
		cwd := sm.cwd
		if len(cwdOverride) > 0 {
			cwd = cwdOverride[0]
		}
		cwd = resolveSessionCWD(cwd)
		session, err = newSessionWithOptions(cwd, nil, "")
		if err != nil {
			return nil, err
		}
		session.path = resolved
		if statErr == nil {
			header, err := marshalSessionLine(session.GetHeader())
			if err != nil {
				return nil, err
			}
			if err := writeSessionLines(resolved, [][]byte{header}); err != nil {
				return nil, err
			}
			session.flushed = true
		}
	} else {
		cwd := session.header.CWD
		if cwd == "" {
			cwd = sm.cwd
		}
		if len(cwdOverride) > 0 {
			cwd = cwdOverride[0]
		}
		cwd = resolveSessionCWD(cwd)
		session.effectiveCWD = &cwd
	}
	session.sessionDir = sm.sessionDir
	sm.mu.Lock()
	sm.current = session
	sm.mu.Unlock()
	return session, nil
}

// resolveSessionCWD resolves an opened session's effective cwd as Pi's
// SessionManager constructor does: resolvePath (tilde and file URL
// normalization, then path.resolve) with no realpath, so a symlinked
// directory such as macOS /var stays as given.
// upstream: packages/coding-agent/src/core/session-manager.ts:SessionManager.constructor
func resolveSessionCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	if resolved, err := ResolvePath(cwd, ""); err == nil {
		return resolved
	}
	return absCleanDir(cwd)
}

func restoreSessionFileEntries(path string, records []FileEntry) (*Session, error) {
	return restoreSessionEntries(path, records, true)
}

// restoreSessionEntries builds the session of a loaded file. persist writes the migrated file back, as upstream _rewriteFile does only for a persisting manager.
func restoreSessionEntries(path string, records []FileEntry, persist bool) (*Session, error) {
	header, valid := records[0].(SessionHeader)
	if !valid {
		return nil, fmt.Errorf("Session file is not a valid pi session: %s", path)
	}
	session, err := newSessionFromEntries(header.CWD, header.ID, records)
	if err != nil {
		return nil, err
	}
	session.path, session.flushed = path, true
	if persist && header.Version < CurrentSessionVersion {
		headerRaw, err := replaceJSONField(records[0].Raw(), "version", CurrentSessionVersion)
		if err != nil {
			return nil, err
		}
		lines := make([][]byte, 0, len(session.slots)+1)
		lines = append(lines, headerRaw)
		// _rewriteFile writes JSON.stringify of every parsed entry, so an entry the migration left unchanged is written in that spelling too.
		for _, entry := range session.allEntriesLocked() {
			line, err := jsonstringify.Canonicalize(entry.Raw())
			if err != nil {
				return nil, err
			}
			lines = append(lines, line)
		}
		if err := writeSessionLines(path, lines); err != nil {
			return nil, err
		}
	}
	return session, nil
}

// errNoSessionRecords and errSessionNeedsMigration end a streaming load early: the file holds no session records, or its version needs the
// whole-file migration.
var (
	errNoSessionRecords      = errors.New("session: no records")
	errSessionNeedsMigration = errors.New("session: needs migration")
)

// loadPersistedSession reads a persisted session file as LoadEntriesFromFile and restoreSessionFileEntries do, but keeps message bodies only
// for the active context (see entrySlot): each other entry is indexed by where its line lies. empty reports a file with no session records, which
// the caller handles as LoadEntriesFromFile's empty result. A file whose version needs migration is migrated and rewritten first.
func loadPersistedSession(path string) (session *Session, empty bool, err error) {
	session, err = streamSessionFile(path)
	switch {
	case errors.Is(err, errNoSessionRecords):
		return nil, true, nil
	case errors.Is(err, errSessionNeedsMigration):
		records, err := LoadEntriesFromFile(path)
		if err != nil {
			return nil, false, err
		}
		if len(records) == 0 {
			return nil, true, nil
		}
		if _, err := restoreSessionFileEntries(path, records); err != nil {
			return nil, false, err
		}
		session, err = streamSessionFile(path)
		if errors.Is(err, errNoSessionRecords) || errors.Is(err, errSessionNeedsMigration) {
			// The rewrite left a file the stream cannot index; keep the migrated entries resident.
			session, err = restoreSessionEntries(path, records, false)
		}
		return session, false, err
	}
	return session, false, err
}

func streamSessionFile(path string) (*Session, error) {
	return streamSessionFileBatched(path, lineBatching{})
}

// streamSessionFileBatched is streamSessionFile, preparing lines as batching says. The zero lineBatching is defaultLineBatching, taken at the
// runtime default width when the file holds more than one batch.
func streamSessionFileBatched(path string, batching lineBatching) (*Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errNoSessionRecords
	}
	if batching == (lineBatching{}) {
		if info.Size() > indexBatchBytes {
			// pig additive (D174): the workers count and run at the runtime default width.
			defer procpolicy.Widen()()
		}
		batching = defaultLineBatching()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nodeerrno.FromPathError(err)
	}
	defer func() { _ = file.Close() }()
	// The lines are read from the snapshot when it is a clone, and copied into it as they are read otherwise, so the snapshot holds the bytes
	// the index locates. Without a snapshot, the records are read back from the session file.
	var source io.Reader = file
	snapshot, cloned := cloneSessionSnapshot(file, path)
	var copier *snapshotCopier
	if cloned {
		source = io.NewSectionReader(snapshot, 0, snapshot.size)
	} else if snapshot, _ = newSessionSnapshot(path); snapshot != nil {
		copier = startSnapshotCopy(snapshot)
		source = io.TeeReader(file, copier)
	}
	kept := false
	defer func() {
		if copier != nil {
			snapshot = copier.finish()
		}
		if !kept && snapshot != nil {
			snapshot.close()
		}
	}()
	reader := &sessionTailReader{Reader: source}
	var s *Session
	var restoreErr error
	index := func(line []byte, off int64, prepared *preparedLine) error {
		if prepared == nil {
			return s.indexLine(line, off)
		}
		return s.applyIndexLine(prepared, line, off)
	}
	err = forEachPreparedLine(reader, batching, func(line []byte, off int64, prepared *preparedLine) error {
		// An object line after the header is valid exactly when its first decode in indexLine reports no syntax error, so it is not validated twice.
		if s != nil && isObjectLine(line) {
			if restoreErr == nil {
				restoreErr = index(line, off, prepared)
			}
			return nil
		}
		if !truthySessionLine(line) {
			return nil
		}
		if s == nil {
			header, ok := sessionentry.DecodeFileEntry(bytes.Clone(line)).(SessionHeader)
			if !ok {
				return errNoSessionRecords
			}
			if header.Version < CurrentSessionVersion {
				return errSessionNeedsMigration
			}
			s = NewSession(header.ID, header.CWD)
			s.header = header
			return nil
		}
		if restoreErr != nil {
			return nil
		}
		restoreErr = index(line, off, prepared)
		return nil
	})
	if err != nil {
		if errors.Is(err, errNoSessionRecords) || errors.Is(err, errSessionNeedsMigration) {
			return nil, err
		}
		if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
			err = nodeerrno.FromPathError(pathErr)
		}
		return nil, err
	}
	if s == nil {
		return nil, errNoSessionRecords
	}
	if copier != nil {
		snapshot, copier = copier.finish(), nil
	}
	if reader.read && reader.last != '\n' {
		if _, err := appendSessionLine(path, nil); err != nil {
			return nil, err
		}
		if snapshot != nil {
			if _, err := snapshot.add([]byte(nil)); err != nil {
				snapshot.close()
				snapshot = nil
			}
		}
	}
	if restoreErr != nil {
		return nil, restoreErr
	}
	s.hasConversation = s.stats.stats.UserMessages > 0 || s.stats.stats.AssistantMessages > 0
	s.path, s.flushed, s.backing, s.snapshot, kept = path, true, path, snapshot, true
	s.setBranchLocked()
	return s, nil
}

// indexBatchBytes is the size of the runs of lines forEachPreparedLine prepares as one piece of work, and indexInFlightBytes bounds the bytes
// of the runs read ahead of the index.
// pig additive (D112): Pi reads the whole file and parses its lines in order on one thread; the index applies the prepared lines in file order, so it holds what the serial load holds.
const (
	indexBatchBytes    = 128 << 10
	indexInFlightBytes = 4 << 20
)

// lineBatching is how forEachPreparedLine splits the work: runs of at least batchBytes, at most depth of them read ahead, on workers
// goroutines.
type lineBatching struct {
	batchBytes, depth, workers int
}

// defaultLineBatching prepares on as many goroutines as may run, within indexInFlightBytes.
func defaultLineBatching() lineBatching {
	depth := indexInFlightBytes / indexBatchBytes
	return lineBatching{batchBytes: indexBatchBytes, depth: depth, workers: min(runtime.GOMAXPROCS(0), depth)}
}

// forEachPreparedLine calls visit with each line of r and its offset, in order, as forEachJSONLLineOffset does. With more than one worker it
// reads ahead of visit and prepares the lines (prepareIndexLine) on the workers, and visit receives each line's preparation; otherwise visit
// receives nil. It returns the first error in line order: visit's, or then the read's. It returns once every goroutine it started has ended.
func forEachPreparedLine(r io.Reader, batching lineBatching, visit func(line []byte, off int64, prepared *preparedLine) error) error {
	depth, workers := batching.depth, batching.workers
	if workers < 2 {
		return forEachJSONLLineOffset(r, func(line []byte, off int64) error { return visit(line, off, nil) })
	}
	// Each batch goes to ordered, for visit in order, and to work, for a worker to prepare; done closes when it is prepared. A batch visit has
	// finished goes back through free for reuse. At most depth batches wait in ordered, so the read stays at most depth+2 batches ahead of visit.
	ordered := make(chan *lineBatch, depth)
	work := make(chan *lineBatch, depth)
	free := make(chan *lineBatch, depth+2)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for batch := range work {
				select {
				case <-stop:
				default:
					batch.prepare()
				}
				close(batch.done) //portlint:allow doubleclose each batch is received from work by exactly one worker, which closes its done once
			}
		})
	}
	wg.Go(func() {
		defer close(work)
		defer close(ordered)
		next := func() *lineBatch {
			select {
			case batch := <-free:
				batch.reset()
				return batch
			default:
				return &lineBatch{done: make(chan struct{})}
			}
		}
		batch := next()
		send := func() bool {
			select {
			case ordered <- batch:
			case <-stop:
				return false
			}
			work <- batch
			batch = next()
			return true
		}
		err := forEachJSONLLineOffset(r, func(line []byte, off int64) error {
			batch.add(line, off)
			if len(batch.buf) >= batching.batchBytes && !send() {
				return errIndexStopped
			}
			return nil
		})
		if errors.Is(err, errIndexStopped) {
			return
		}
		batch.err = err
		send()
	})
	var err error
	for batch := range ordered {
		<-batch.done
		for k, line := range batch.lines {
			if err = visit(batch.buf[line.start:line.end], line.off, &batch.prepared[k]); err != nil {
				break
			}
		}
		if err == nil {
			err = batch.err
		}
		if err != nil {
			break
		}
		// No more than depth+2 batches exist: one being filled, depth waiting in ordered and one being visited; a batch is made only when free
		// is empty. So free always has room.
		free <- batch
	}
	close(stop)
	for range ordered {
	}
	wg.Wait()
	return err
}

// errIndexStopped ends the read of forEachPreparedLine once visit has returned an error.
var errIndexStopped = errors.New("session: index stopped")

// lineBatch is a run of consecutive lines with their offsets, copied out of the read, and their preparations once done is closed. err is the
// read's error after the last line.
type lineBatch struct {
	buf      []byte
	lines    []batchLine
	prepared []preparedLine
	err      error
	done     chan struct{}
}

type batchLine struct {
	start, end int
	off        int64
}

func (b *lineBatch) add(line []byte, off int64) {
	start := len(b.buf)
	b.buf = append(b.buf, line...)
	b.lines = append(b.lines, batchLine{start: start, end: len(b.buf), off: off})
}

func (b *lineBatch) prepare() {
	b.prepared = slices.Grow(b.prepared[:0], len(b.lines))[:len(b.lines)]
	for k, line := range b.lines {
		b.prepared[k] = prepareIndexLine(b.buf[line.start:line.end])
	}
}

func (b *lineBatch) reset() {
	clear(b.prepared)
	b.buf, b.lines, b.prepared, b.err, b.done = b.buf[:0], b.lines[:0], b.prepared[:0], nil, make(chan struct{})
}

// indexLine adds the record of one line after the header as newSessionFromEntries adds a decoded record. A message or custom message is not
// kept decoded; setBranchLocked reads back the ones the active context needs.
func (s *Session) indexLine(line []byte, off int64) error {
	prepared := prepareIndexLine(line)
	return s.applyIndexLine(&prepared, line, off)
}

// preparedLine is the part of indexing one line that depends on that line alone (prepareIndexLine), so lines may be prepared in any order
// and on any goroutine. applyIndexLine adds it to the index; lines are applied in file order.
type preparedLine struct {
	state preparedLineState
	// base is a message's base members, and slot its slot without a cold part.
	base SessionEntryBase
	slot entrySlot
	// account is the accounting decode of a message, added to the stats when counted.
	account sessionAccountingEntry
	counted bool
	facts   *treeFacts
	hash    uint64
	// entry is the decoded record of a line that is no message.
	entry SessionEntry
}

type preparedLineState uint8

const (
	// lineSkipped is a line the load skips: JSON.parse throws on it, or it is the record of no entry or a second header.
	lineSkipped preparedLineState = iota
	lineMessage
	lineEntry
)

// prepareIndexLine reads what indexing line needs from the line alone. One decode serves the base members and the accounting when both
// decode; otherwise each decodes on its own, as the full load does.
func prepareIndexLine(line []byte) preparedLine {
	if !jsonvalid.Valid(line) {
		// JSON.parse throws on the line, so the load skips it.
		return preparedLine{}
	}
	probe, ok := walkIndexProbe(line)
	var err error
	if !ok {
		err = json.Unmarshal(line, &probe)
	}
	if err == nil && (probe.Type == "message" || probe.Type == "custom_message") {
		if timestamp, ok := rawJSONString(probe.Timestamp); ok {
			base := sessionentry.WithJSValues(line, SessionEntryBase{Type: probe.Type, ID: probe.ID, ParentID: probe.ParentID, Timestamp: timestamp})
			return prepareMessageLine(base, probe.sessionAccountingEntry, true, line)
		}
	}
	// A record the probe read as another entry type decodes once, as that type: its type is not a message's, and not the header's.
	other := err == nil && probe.Type != "message" && probe.Type != "custom_message" && probe.Type != "session"
	var base SessionEntryBase
	if !other && json.Unmarshal(line, &base) == nil && (base.Type == "message" || base.Type == "custom_message") {
		var account sessionAccountingEntry
		decoded := json.Unmarshal(line, &account) == nil
		return prepareMessageLine(sessionentry.WithJSValues(line, base), account, decoded, line)
	}
	var entry SessionEntry
	if other {
		entry = sessionentry.DecodeSessionEntry(bytes.Clone(line))
	} else {
		var isEntry bool
		if entry, isEntry = sessionentry.DecodeFileEntry(bytes.Clone(line)).(SessionEntry); !isEntry {
			return preparedLine{}
		}
	}
	if entry.Base().Type == "session" {
		return preparedLine{}
	}
	return preparedLine{state: lineEntry, entry: entry}
}

// prepareMessageLine prepares a message or custom message line, which the index keeps without its body. account is its accounting decode,
// when decoded. The /tree search text is read when /tree first needs it (indexSearchTexts, after the selector's first paint), not here.
func prepareMessageLine(base SessionEntryBase, account sessionAccountingEntry, decoded bool, line []byte) preparedLine {
	p := preparedLine{state: lineMessage, base: base, slot: baseSlot(base), account: account, counted: decoded, hash: recordHash(line)}
	if base.Type == "message" {
		if decoded {
			p.slot.setMessageKind(messageKindFrom(account.Message.Role, account.Message.API))
		} else {
			p.slot.setMessageKind(rawMessageKind(line))
		}
		if p.slot.role() == "assistant" {
			// The /tree selector classifies every row before its first paint; reading an assistant's facts here, from the line in hand, spares
			// that paint a second pass over the file.
			p.facts = treeFactsOfLine(line)
		}
	}
	return p
}

// applyIndexLine adds the prepared record of line, at off in the file, to the index.
func (s *Session) applyIndexLine(p *preparedLine, line []byte, off int64) error {
	switch p.state {
	case lineMessage:
		if p.counted {
			s.stats.addDecoded(p.account, p.base.Type)
		}
		slot := p.slot
		s.locateHashedLocked(&slot, off, len(line), p.hash)
		if slot.role() == "assistant" {
			slot.facts = s.storeFactsLocked(p.facts)
		}
		s.noteTimestampLocked(s.appendSlotLocked(slot, p.base.ParentID, line), p.base.Timestamp)
		s.leafID = leafOf(p.base.ID)
	case lineEntry:
		entry := p.entry
		base := entry.Base()
		if accountsRecord(base.Type) {
			s.stats.add(entry.Raw(), base.Type)
		}
		s.noteNameLocked(entry.Raw(), base.Type)
		slot := newSlot(compactionWithoutRecord(entry, line))
		s.locateLocked(&slot, off, line)
		s.addSlotLocked(slot, base.ParentID, line)
		s.leafID = leafOf(base.ID)
	}
	return nil
}

// accountsRecord reports whether the session accounting reads a record of entryType (sessionAccountingAccumulator.addDecoded); a record of
// another type does not change it.
func accountsRecord(entryType string) bool {
	switch entryType {
	case "usage", "compaction", "branch_summary", "message", "bash_execution":
		return true
	}
	return false
}

// leafOf is the leaf after an entry with id is added: _buildIndex and _appendEntry set leafId to the entry's id, so an entry whose id is null
// leaves the leaf null.
func leafOf(id string) *string {
	if sessionentry.IsNull(id) {
		return nil
	}
	return &id
}

// rawJSONString is the string a SessionEntryBase string member decodes to from raw: absent and null are "", a JSON string is its value, and
// any other value does not decode.
func rawJSONString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var value string
	if raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
