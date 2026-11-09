package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/node/app/uploads.ts
//
// Chunked uploads of one `Files` object (slice 13 §4, design §23 "Chunked transfers"): each upload one file handle in the
// run's directory, written at positions. An append is accepted at the offset the accepted appends reached, so a writer may
// send several without waiting (the call window allows it): each write lands at its own position whatever order the
// scheduler completes them in. `uploaded`, `finish` and expiry wait for an upload's writes in flight; a failed write ends
// its upload.
//
// Go forced differences: Pi's `append` accepts the bytes and starts the write synchronously, then awaits it; Accept is that
// first half (it returns the write to run) and Append both. The writes in flight are a WaitGroup, not a Set of Promises.

// upload is an upload under way: its owner's data, its file, the bytes accepted, its writes in flight, its first failed
// write, its end.
type upload[T any] struct {
	data    T
	file    *os.File
	size    int64
	touched time.Time
	writes  sync.WaitGroup
	failed  bool
	ended   sync.Once
}

// Finished is a finished upload: what it was begun with and its size, its bytes moved to the path Finish was given.
type Finished[T any] struct {
	Data T
	Size int64
}

// Uploads are the uploads under way of one `Files` object, in the directory run (this object's own, made on first use), at
// most max at once: each holds a file handle until it ends, so a caller beginning uploads without end would exhaust the
// process's descriptors. Each carries its owner's T (what the file becomes when it finishes).
type Uploads[T any] struct {
	run string
	max int

	mu      sync.Mutex
	uploads map[string]*upload[T]
	// order is the ids in the order they began (a Map's iteration order).
	order []string
}

// NewUploads is the uploads of directory run, at most max under way.
func NewUploads[T any](run string, max int) *Uploads[T] {
	return &Uploads[T]{run: run, max: max, uploads: map[string]*upload[T]{}}
}

// Size is the bytes accepted by every upload under way (they count against the host's cap).
func (uploads *Uploads[T]) Size() int64 {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	return uploads.sizeLocked()
}

// sizeLocked is Size for a caller holding the lock.
func (uploads *Uploads[T]) sizeLocked() int64 {
	var size int64
	for _, each := range uploads.uploads {
		size += each.size
	}
	return size
}

// Find is the id of the upload under way whose data matches, if any.
func (uploads *Uploads[T]) Find(matches func(data T) bool) (string, bool) {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	for _, id := range uploads.order {
		if matches(uploads.uploads[id].data) {
			return id, true
		}
	}
	return "", false
}

// Begin is a new upload carrying data: its id; `unavailable` (`busy`) while max are under way.
func (uploads *Uploads[T]) Begin(data T) (string, error) {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	if len(uploads.uploads) >= uploads.max {
		return "", errBusy(uploads.max)
	}
	id := NewToken(12)
	if err := os.MkdirAll(uploads.run, 0o700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(filepath.Join(uploads.run, id), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	//portlint:allow clock the package has no clock seam (its one func() time.Time field, App.processStart, is the program start for an edit check); uploads.ts reads `touched: Date.now()` (uploads.ts:72)
	uploads.uploads[id] = &upload[T]{data: data, file: file, touched: time.Now()}
	uploads.order = append(uploads.order, id)
	return id, nil
}

func errBusy(max int) error {
	return data.NewChordError(data.ChordErrorInit{
		Code: data.CodeUnavailable, Reason: data.ReasonBusy,
		Message: fmt.Sprintf("files: %d uploads are under way; finish one, or wait for an idle one to expire", max),
	}, nil)
}

// AcceptedWrite is an append accepted and not yet written: Run writes it at its own position.
type AcceptedWrite struct {
	run func() error
}

// Run writes the accepted bytes; a failed write ends the upload and is returned.
func (accepted *AcceptedWrite) Run() error { return accepted.run() }

// Accept takes bytes at offset of upload id, which must be where the accepted appends reached (else `invalid`, its details
// the expected `{ offset }`); room(adding, fileSize) refuses what the caps do not allow, before anything is accepted. The
// bytes count at once, so the next append may follow without waiting: its write goes to its own position.
func (uploads *Uploads[T]) Accept(id string, offset int64, bytes []byte, room func(adding, fileSize int64) error) (*AcceptedWrite, error) {
	return uploads.accept(id, offset, bytes, func(adding, fileSize, _ int64) error { return room(adding, fileSize) })
}

// accept is Accept with room also given the bytes every upload under way holds (this one's accepted bytes included), which
// the caller cannot ask for while the uploads are locked.
func (uploads *Uploads[T]) accept(id string, offset int64, bytes []byte, room func(adding, fileSize, underway int64) error) (*AcceptedWrite, error) {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	each, err := uploads.underway(id)
	if err != nil {
		return nil, err
	}
	if offset != each.size {
		return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("files: upload %s is at offset %d, not %d", id, each.size, offset),
			delta.JsonObjectOf("offset", float64(each.size)))
	}
	if err := room(int64(len(bytes)), each.size+int64(len(bytes)), uploads.sizeLocked()); err != nil {
		return nil, err
	}
	each.size += int64(len(bytes))
	//portlint:allow clock the package has no clock seam (its one func() time.Time field, App.processStart, is the program start for an edit check); uploads.ts reads `upload.touched = Date.now()` (uploads.ts:102)
	each.touched = time.Now()
	each.writes.Add(1)
	return &AcceptedWrite{run: func() error {
		defer each.writes.Done()
		if _, err := each.file.WriteAt(bytes, offset); err != nil {
			uploads.fail(id, each)
			return err
		}
		return nil
	}}, nil
}

// fail ends upload id after a failed write: the first failure removes it.
func (uploads *Uploads[T]) fail(id string, each *upload[T]) {
	uploads.mu.Lock()
	first := !each.failed
	each.failed = true
	if first {
		uploads.remove(id)
	}
	uploads.mu.Unlock()
	if first {
		// The failed write is itself in flight: the rest are waited for by closing, not by this one.
		_ = each.file.Close()
		_ = os.Remove(filepath.Join(uploads.run, id))
	}
}

// Append is Accept and the write of what it accepted.
func (uploads *Uploads[T]) Append(id string, offset int64, bytes []byte, room func(adding, fileSize int64) error) error {
	accepted, err := uploads.Accept(id, offset, bytes, room)
	if err != nil {
		return err
	}
	return accepted.Run()
}

// Uploaded is the bytes of upload id written so far, once its writes in flight landed: where a writer resumes.
func (uploads *Uploads[T]) Uploaded(id string) (int64, error) {
	uploads.mu.Lock()
	each, err := uploads.underway(id)
	uploads.mu.Unlock()
	if err != nil {
		return 0, err
	}
	each.writes.Wait()
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	if each.failed {
		return 0, failedUpload(id)
	}
	return each.size, nil
}

// Finish ends upload id once its writes landed and moves its file to path; refused when a write of it failed.
func (uploads *Uploads[T]) Finish(id, path string) (Finished[T], error) {
	uploads.mu.Lock()
	each, err := uploads.underway(id)
	if err != nil {
		uploads.mu.Unlock()
		return Finished[T]{}, err
	}
	// Taken out first: an append arriving now finds no upload.
	uploads.remove(id)
	uploads.mu.Unlock()
	if err := uploads.end(each); err != nil {
		return Finished[T]{}, err
	}
	uploads.mu.Lock()
	failed := each.failed
	uploads.mu.Unlock()
	if failed {
		return Finished[T]{}, failedUpload(id)
	}
	if err := os.Rename(filepath.Join(uploads.run, id), path); err != nil {
		return Finished[T]{}, err
	}
	return Finished[T]{Data: each.data, Size: each.size}, nil
}

// Cancel drops upload id once its writes landed (an unknown one: nothing): its writer gave up.
func (uploads *Uploads[T]) Cancel(id string) error {
	uploads.mu.Lock()
	each, ok := uploads.uploads[id]
	uploads.mu.Unlock()
	if !ok {
		return nil
	}
	return uploads.discard(id, each)
}

// Expire drops the uploads unwritten for idle, once their writes landed.
func (uploads *Uploads[T]) Expire(idle time.Duration) error {
	//portlint:allow clock the package has no clock seam (its one func() time.Time field, App.processStart, is the program start for an edit check); uploads.ts reads `const now = Date.now()` (uploads.ts:147)
	now := time.Now()
	for _, id := range uploads.ids() {
		uploads.mu.Lock()
		each, ok := uploads.uploads[id]
		due := ok && now.Sub(each.touched) >= idle
		uploads.mu.Unlock()
		if due {
			if err := uploads.discard(id, each); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close drops every upload under way (their object ended).
func (uploads *Uploads[T]) Close() error {
	for _, id := range uploads.ids() {
		uploads.mu.Lock()
		each, ok := uploads.uploads[id]
		uploads.mu.Unlock()
		if ok {
			if err := uploads.discard(id, each); err != nil {
				return err
			}
		}
	}
	return nil
}

func (uploads *Uploads[T]) ids() []string {
	uploads.mu.Lock()
	defer uploads.mu.Unlock()
	return append([]string(nil), uploads.order...)
}

// underway is the upload id under way; else `invalid`. The caller holds the lock.
func (uploads *Uploads[T]) underway(id string) (*upload[T], error) {
	each, ok := uploads.uploads[id]
	if !ok {
		return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("files: no upload %s (finished, failed, expired or unknown)", id))
	}
	return each, nil
}

// remove takes id out of the uploads. The caller holds the lock.
func (uploads *Uploads[T]) remove(id string) {
	delete(uploads.uploads, id)
	for index, each := range uploads.order {
		if each == id {
			uploads.order = append(uploads.order[:index:index], uploads.order[index+1:]...)
			return
		}
	}
}

// discard takes upload id out, closes its file once its writes landed and removes it.
func (uploads *Uploads[T]) discard(id string, each *upload[T]) error {
	uploads.mu.Lock()
	uploads.remove(id)
	uploads.mu.Unlock()
	if err := uploads.end(each); err != nil {
		return err
	}
	return os.Remove(filepath.Join(uploads.run, id))
}

// end closes upload's file once its writes in flight landed; once, whoever ends it first.
func (uploads *Uploads[T]) end(each *upload[T]) error {
	var err error
	each.ended.Do(func() {
		each.writes.Wait()
		err = each.file.Close()
	})
	return err
}

// failedUpload is the refusal of an upload one of whose writes failed.
func failedUpload(id string) error {
	return data.Invalid(data.ReasonArgs, fmt.Sprintf("files: upload %s failed to write and was dropped", id))
}
