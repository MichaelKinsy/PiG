package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
)

// The intent of an import or a delete of an address's data (D462).
//
// Pi's server part of an import or a delete is one commit (`host-data.ts:316`), so a kill leaves the address's server data all old or
// all new. PiG's is two, the NodeHost's store and the Go host's, and nothing commits across two SQLite stores: neither can hold a record
// the other's commit clears. The record is a file in `server/imports/`, written durably BEFORE the first commit and removed when the
// operation ends, so a start finds it exactly when a process was killed (or failed to remove it) in between:
//
//   - `<kind>.<key>.json` is the intent: what the operation is (`import` or `delete`), the address, and for an import the names of its
//     staging directory and of where the old directory was set aside;
//   - `<kind>.<key>.node` and `.go` are, for an import, the address's data as the NodeHost and the Go host held it before (the parts
//     `ExportHostData` writes), so that the undo is the same whatever the import's mode: a replace of the address's data by its old part.
//
// At start (resolveIntents) an import is UNDONE, with the code of the live rollback (restoreStores, placement.undo): both stores are
// restored from the saved parts, the new directory or the staging directory is removed, the old directory comes back from where it
// was set aside, and the intent goes last. It is undone and never completed because a kill cannot say which commit ran (and an import
// that refuses an address that has data would read its own first commit as "has data"). A kill after the second commit and before the
// intent's removal therefore undoes a finished import: its caller saw no success. A delete is completed (completeDelete): the
// request's own end state, which touches only that address and which every step reaches again without harm.
//
// An intent that cannot be used (it does not parse, names another address, its saved parts are missing or do not restore, a store
// fails again) is never guessed at: it blocks its address, whose worker fails to start and whose import, delete and export are refused
// with an error that names the file, and the other addresses serve; the operator is told by an OnError report of kind
// AppErrorInterruptedDataChange and the intent stays for the next start. A broken address fails its own worker the same way today
// (`worker failed to start`), not the server.
//
// There is one intent per address: the operations of an address run under the worker's lock.

// The kinds of intent.
const (
	intentImport = "import"
	intentDelete = "delete"
)

// dataIntent is the content of an intent file: one strict shape (Pig's own format has no version).
type dataIntent struct {
	Kind string `json:"kind"`
	// Address is the address as AddressParam writes it.
	Address json.RawMessage `json:"address"`
	// Staging and Aside are the names, in the worker kind's directory, of an import's staging directory and of where the old
	// directory is set aside ("" for none). Names, so that a data directory that moved between the kill and the start is still found.
	Staging string `json:"staging,omitempty"`
	Aside   string `json:"aside,omitempty"`
}

// intentsDirectory is where the intents of dataDir are. It does not exist until the first import or delete.
func intentsDirectory(dataDir string) string { return filepath.Join(dataDir, "server", "imports") }

// intentStem names the files of address's intent: its kind and its key, encoded as its directory's name is.
func intentStem(address contracts.Address) string {
	return address.Kind + "." + encodedKey(address.Key)
}

// intentFiles are the files of one intent.
type intentFiles struct{ dir, stem string }

func (f intentFiles) record() string   { return filepath.Join(f.dir, f.stem+".json") }
func (f intentFiles) nodePart() string { return filepath.Join(f.dir, f.stem+".node") }
func (f intentFiles) goPart() string   { return filepath.Join(f.dir, f.stem+".go") }
func (f intentFiles) temp() string     { return filepath.Join(f.dir, f.stem+tempSuffix) }

const (
	stagingPrefix = ".chord-import-"
	tempSuffix    = ".json.tmp"
)

var (
	// syncFile and syncDirectory are how the intent reaches the disk; a test replaces them to see the order of the flushes around the
	// commits (no kill of a process shows a flush: the page cache survives it).
	syncFile      = func(file *os.File) error { return file.Sync() }
	syncDirectory = flushDirectory
)

// resolving tells resolveSeam of a step.
func resolving(step string) {
	if resolveSeam != nil {
		resolveSeam(step)
	}
}

// blockedAddresses are the addresses whose interrupted import or delete the start could not resolve, by intent stem, with the error each
// refuses its requests with.
type blockedAddresses struct {
	mu   sync.Mutex
	errs map[string]error
}

func (b *blockedAddresses) set(stem string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.errs == nil {
		b.errs = map[string]error{}
	}
	b.errs[stem] = err
}

func (b *blockedAddresses) get(address contracts.Address) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errs[intentStem(address)]
}

// recordIntent writes the intent of an operation on address, durably, and for an import the old parts of both hosts beside it: when it
// returns the operation may begin. A failure removes what it wrote and refuses the operation, with nothing changed.
func (a *App) recordIntent(address contracts.Address, intent dataIntent, ctx context.Context) (files intentFiles, err error) {
	files = intentFiles{dir: intentsDirectory(a.DataDir), stem: intentStem(address)}
	defer func() {
		if err != nil {
			forgetIntent(files)
		}
	}()
	existed := Exists(files.dir)
	if err := PrivateDirectory(files.dir); err != nil {
		return files, err
	}
	if !existed {
		if err := syncDirectory(filepath.Dir(files.dir)); err != nil {
			return files, err
		}
	}
	// An intent a failed removal left is replaced by this one: its operation was reported to its caller as failed.
	if err := os.Remove(files.record()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return files, err
	}
	if intent.Kind == intentImport {
		if err := a.saveOldParts(files, address, ctx); err != nil {
			return files, err
		}
	}
	intent.Address = json.RawMessage(AddressParam(address))
	text, err := json.Marshal(intent)
	if err != nil {
		return files, err
	}
	return files, writeDurably(files, text)
}

// saveOldParts writes the NodeHost's and the Go host's data of address, as they are now, to the intent's part files (flushed).
func (a *App) saveOldParts(files intentFiles, address contracts.Address, ctx context.Context) error {
	if err := a.nodeData.ExportHostData(address, files.nodePart(), ctx); err != nil {
		return err
	}
	if err := flushFile(files.nodePart()); err != nil {
		return err
	}
	exported, err := a.Host.ExportHostData(address, ctx)
	if err != nil {
		return err
	}
	if err := writeParts(ctx, exported, files.goPart()); err != nil {
		return err
	}
	return flushFile(files.goPart())
}

// flushFile makes the file at path durable.
func flushFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(syncFile(file), file.Close())
}

// writeDurably writes text as the intent's record (mode 0600): through a temporary file that is flushed and renamed, then the directory is
// flushed, so that a reader, a kill or a power cut finds the whole file or none under its name.
func writeDurably(files intentFiles, text []byte) (err error) {
	temp, path := files.temp(), files.record()
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(temp)
		}
	}()
	if _, err := file.Write(text); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := errors.Join(syncFile(file), file.Close()); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// forgetIntent removes the files of an intent, whatever there is of them.
func forgetIntent(files intentFiles) {
	for _, path := range []string{files.record(), files.temp(), files.nodePart(), files.goPart()} {
		_ = os.Remove(path)
	}
}

// finishIntent ends the operation of an intent: the record is removed and the removal made durable, then the part files go. The record
// is what says the operation is under way, so it goes first: a failure leaves it (and the parts it needs) for the next start.
func (a *App) finishIntent(files intentFiles) error {
	resolving("intent")
	if err := os.Remove(files.record()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := syncDirectory(files.dir); err != nil {
		return err
	}
	forgetIntent(files)
	return nil
}

// dropped is err with the failure of finishIntent joined to it: an operation that ends in an error ends its intent as well.
func (a *App) dropped(files intentFiles, err error) error {
	if removed := a.finishIntent(files); removed != nil {
		return errors.Join(err, removed)
	}
	return err
}

// placement is where an import's directories are: the worker's directory, the staging directory the new one came from, and where the old
// one is set aside ("" when there was none).
type placement struct{ dir, staging, aside string }

// undo puts the worker's directory back as it was before the import, whichever of its renames ran: the staging directory goes (it is
// the new directory once placed); where an old directory was set aside and is there, the new directory goes and the old one comes back;
// where none was, no directory is the old state. Each step is safe to repeat.
func (p placement) undo() error {
	resolving("place")
	err := os.RemoveAll(p.staging)
	if p.aside == "" {
		return errors.Join(err, os.RemoveAll(p.dir))
	}
	if !Exists(p.aside) {
		// The old directory was never moved, or is back already: the directory is the old one.
		return err
	}
	if removed := os.RemoveAll(p.dir); removed != nil {
		return errors.Join(err, removed)
	}
	return errors.Join(err, os.Rename(p.aside, p.dir))
}

// restoreStores puts the address's data back, in the NodeHost's store (node) and the Go host's (goHost), as the intent's saved parts hold
// it: a replace of the address's data by its old part, or its removal when the old part holds nothing. It runs whatever became of ctx: an
// abort must not leave the two stores apart.
func (a *App) restoreStores(files intentFiles, address contracts.Address, node, goHost bool, ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	if node {
		resolving("restore-node")
		if err := a.restoreNode(files.nodePart(), address, ctx); err != nil {
			return fmt.Errorf("restoring the plugins' store from %s: %w", files.nodePart(), err)
		}
	}
	if goHost {
		resolving("restore-go")
		if err := a.restoreGo(files.goPart(), address, ctx); err != nil {
			return fmt.Errorf("restoring the server host's store from %s: %w", files.goPart(), err)
		}
	}
	return nil
}

func (a *App) restoreNode(part string, address contracts.Address, ctx context.Context) error {
	held, err := holdsRecords(part, ctx)
	if err != nil {
		return err
	}
	if held {
		return a.nodeData.ImportHostData(address, part, store.ExistingReplace, ctx)
	}
	return a.nodeData.DeleteHostData(address, ctx)
}

func (a *App) restoreGo(part string, address contracts.Address, ctx context.Context) error {
	held, err := holdsRecords(part, ctx)
	if err != nil {
		return err
	}
	if !held {
		_, err := a.Host.DeleteHostData(address, ctx, host.HostDeleteOptions{})
		return err
	}
	file, err := os.Open(part)
	if err != nil {
		return err
	}
	_, err = a.Host.ImportHostData(store.DecodeJsonl(&fileBytes{file: file}), ctx, store.HostImportOptions{Address: &address, Existing: store.ExistingReplace})
	return err
}

// holdsRecords is whether the host data archive at path holds anything but its manifest and its end.
func holdsRecords(path string, ctx context.Context) (bool, error) {
	source, err := openParts(path)
	if err != nil {
		return false, err
	}
	parts := partsOf(source)
	defer func() { _ = parts.Close() }()
	for {
		part, ok, err := parts.Next(ctx)
		if err != nil || !ok {
			return false, err
		}
		switch part.(type) {
		case *store.ManifestPart, *store.EndPart:
		default:
			return true, nil
		}
	}
}

// completeDelete is the steps of a delete after its intent: the NodeHost's rows, the Go host's, then the directory. It is the live
// delete's and the start's completion of an interrupted one.
func (a *App) completeDelete(address contracts.Address, dir string, ctx context.Context) error {
	resolving("delete-node")
	if err := a.nodeData.DeleteHostData(address, ctx); err != nil {
		return err
	}
	resolving("delete-go")
	if _, err := a.Host.DeleteHostData(address, ctx, host.HostDeleteOptions{}); err != nil {
		return err
	}
	resolving("delete-directory")
	return os.RemoveAll(dir)
}

// readIntent reads and checks the intent of files: its address, the placement it names and the address it is for. A record that is not
// exactly the shape written here is refused, not interpreted.
func (a *App) readIntent(files intentFiles) (dataIntent, contracts.Address, placement, error) {
	var intent dataIntent
	text, err := os.ReadFile(files.record())
	if err != nil {
		return intent, contracts.Address{}, placement{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil {
		return intent, contracts.Address{}, placement{}, fmt.Errorf("it is not an intent: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return intent, contracts.Address{}, placement{}, errors.New("it is not an intent: text follows it")
	}
	address, err := parseAddress(string(intent.Address))
	if err != nil {
		return intent, address, placement{}, fmt.Errorf("it names no address: %w", err)
	}
	if address.Kind == "" || address.Key == "" {
		return intent, address, placement{}, errors.New("it names no address")
	}
	if intentStem(address) != files.stem {
		return intent, address, placement{}, fmt.Errorf("it is for %s, not the address its name says", address.Key)
	}
	where := placement{dir: WorkerDirectory(a.DataDir, address)}
	parent := filepath.Dir(where.dir)
	plain := func(name, prefix string) bool { return strings.HasPrefix(name, prefix) && filepath.Base(name) == name }
	switch {
	case intent.Kind == intentImport && plain(intent.Staging, stagingPrefix) && (intent.Aside == "" || plain(intent.Aside, filepath.Base(where.dir)+".aside-")):
		where.staging = filepath.Join(parent, intent.Staging)
		if intent.Aside != "" {
			where.aside = filepath.Join(parent, intent.Aside)
		}
	case intent.Kind == intentDelete && intent.Staging == "" && intent.Aside == "":
	default:
		return intent, address, placement{}, errors.New("its kind, staging directory or aside directory is not one this server wrote")
	}
	return intent, address, where, nil
}

// resolveIntents resolves the intents an earlier process left (a kill, or a removal that failed), before the server serves anything.
// Without a `server/imports` directory it does nothing.
//
// pig divergence (D462): PiG's import and delete of an address's server data are two commits where Pi's are one; see the intent's comment.
func (a *App) resolveIntents(ctx context.Context) error {
	dir := intentsDirectory(a.DataDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start: the interrupted imports of %s cannot be listed: %w", dir, err)
	}
	// Resolving is not abortable: a start whose ctx ends halfway would leave the two stores apart.
	ctx = context.WithoutCancel(ctx)
	have := map[string]bool{}
	for _, entry := range entries {
		have[entry.Name()] = true
	}
	var stems []string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".json"):
			stems = append(stems, strings.TrimSuffix(name, ".json"))
		case strings.HasSuffix(name, tempSuffix):
			// A record that was never renamed into place: the operation had not begun.
			_ = os.Remove(filepath.Join(dir, name))
		case strings.HasSuffix(name, ".node") && !have[strings.TrimSuffix(name, ".node")+".json"], strings.HasSuffix(name, ".go") && !have[strings.TrimSuffix(name, ".go")+".json"]:
			// Saved parts of an operation whose record never became durable: it had not begun.
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	for _, stem := range stems {
		a.resolveIntent(intentFiles{dir: dir, stem: stem}, ctx)
	}
	resolving("resolved")
	return nil
}

// resolveIntent resolves one intent and reports what it did; an intent it cannot resolve blocks its address.
func (a *App) resolveIntent(files intentFiles, ctx context.Context) {
	intent, address, where, err := a.readIntent(files)
	what := "an interrupted import or delete of " + files.stem
	if err == nil {
		what = fmt.Sprintf("the interrupted %s of %s", intent.Kind, address.Key)
		// Safe to clear here: openServer holds the data directory's server lock and no worker of this server has started yet,
		// so this start is the sole clearer for this address's lock (no two-clearers race). A live worker still holding the
		// lock is untouched because ClearStaleLock removes a socket file only when nobody accepts on it (underLock then waits
		// and refuses if still held).
		if _, err = workers.ClearStaleLock(workerLock(a.options.Name, a.DataDir, address.Key), ctx); err == nil {
			err = a.underLock(address, "resolve", "holds its worker lock", ctx, func() error { return a.resolve(intent, address, where, files, ctx) })
		}
	}
	if err != nil {
		blocked := fmt.Errorf("start: %s cannot be resolved: %w. Its record is %s. Until it is resolved the address's worker does not start and its import, delete and export are refused; its data is as it was before the change or partly changed: check it, then fix the cause and restart the server, or remove the record and its .node and .go files", what, err, files.record())
		a.blocked.set(files.stem, blocked)
		a.report(AppErrorInterruptedDataChange, blocked)
		return
	}
	if intent.Kind == intentDelete {
		a.report(AppErrorInterruptedDataChange, fmt.Errorf("start: %s was completed: the address's directory, records and rows are gone", what))
		return
	}
	a.report(AppErrorInterruptedDataChange, fmt.Errorf("start: %s was undone: the address's directory, records and rows are as they were before it", what))
}

// resolve undoes an interrupted import with the live rollback's code, or completes an interrupted delete, and ends the intent.
func (a *App) resolve(intent dataIntent, address contracts.Address, where placement, files intentFiles, ctx context.Context) error {
	if intent.Kind == intentDelete {
		if err := a.completeDelete(address, where.dir, ctx); err != nil {
			return err
		}
		return a.finishIntent(files)
	}
	if err := a.restoreStores(files, address, true, true, ctx); err != nil {
		return err
	}
	if err := where.undo(); err != nil {
		return err
	}
	return a.finishIntent(files)
}
