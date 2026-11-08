package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/sync/filesystem.ts

// readChunk is how much of a body is read from its file at a time.
const readChunk = 256 * 1024

var (
	slotName     = regexp.MustCompile(`^(\d{16})\.json$`)
	snapshotName = regexp.MustCompile(`^(\d{16})\.batch$`)
	sessionIDPat = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)
)

func pad(value Seq) string { return fmt.Sprintf("%016d", value) }

// CheckSessionID refuses a session ID that could leave the root of a directory it names a file or directory in.
func CheckSessionID(sessionID string) (string, error) {
	if !sessionIDPat.MatchString(sessionID) {
		return "", fmt.Errorf("Invalid session ID for a sync directory: %s", sessionID)
	}
	return sessionID, nil
}

// FileSystemSyncRemote is a remote store in a directory, laid out as the real remote is: per session, one slot per batch
// start, holding where the batch ends and which device created it, and snapshots named by their end. As the real remote,
// it creates a slot only where the history ends. Creating a slot is check-then-write, without the atomicity of the real
// remote across processes: for development and tests, with one process writing.
//
//	{root}/{session}/slots/{after}.json     { after, to, device, archive?, folded? }, written once
//	{root}/{session}/batches/{after}.batch  the commits (or an archive), until folded
//	{root}/{session}/snapshots/{to}.batch   a store archive
//	{root}/{session}/meta.json              the host's description, last write wins
type FileSystemSyncRemote struct {
	fs   SyncFileSystem
	root string
}

var _ SessionSyncMaintenance = (*FileSystemSyncRemote)(nil)

// NewFileSystemSyncRemote is the remote in directory root of fs.
func NewFileSystemSyncRemote(fs SyncFileSystem, root string) *FileSystemSyncRemote {
	return &FileSystemSyncRemote{fs: fs, root: root}
}

// slotFile is a slot as its file holds it, in the order Pi writes its members.
type slotFile struct {
	After   Seq    `json:"after"`
	To      Seq    `json:"to"`
	Device  string `json:"device"`
	Archive bool   `json:"archive,omitempty"`
	Folded  bool   `json:"folded,omitempty"`
}

func (remote *FileSystemSyncRemote) PutBatch(ctx context.Context, sessionID string, after, to Seq, device string, body SyncBody, options PutBatchOptions) (PutBatchResult, error) {
	existing, err := remote.slot(ctx, sessionID, after)
	if err != nil {
		return PutBatchResult{}, err
	}
	if existing != nil {
		return PutBatchResult{Type: PutExists, To: existing.To, Device: existing.Device}, nil
	}
	listing, err := remote.List(ctx, sessionID)
	if err != nil {
		return PutBatchResult{}, err
	}
	if head := RemoteHead(listing); after != head {
		return PutBatchResult{Type: PutNotHead, Head: head}, nil
	}
	if err := remote.write(ctx, sessionID, []string{"batches"}, pad(after)+".batch", body); err != nil {
		return PutBatchResult{}, err
	}
	// The slot is written last: it is what makes the batch exist.
	if err := remote.writeSlot(ctx, sessionID, BatchInfo{After: after, To: to, Device: device, Archive: options.Archive}); err != nil {
		return PutBatchResult{}, err
	}
	return PutBatchResult{Type: PutCreated}, nil
}

func (remote *FileSystemSyncRemote) GetBatch(ctx context.Context, sessionID string, after Seq) (SyncBody, error) {
	slot, err := remote.slot(ctx, sessionID, after)
	if err != nil || slot == nil || slot.Folded {
		return nil, err
	}
	return remote.stream(ctx, sessionID, "batches", pad(after)+".batch")
}

func (remote *FileSystemSyncRemote) GetSnapshot(ctx context.Context, sessionID string, to Seq) (SyncBody, error) {
	return remote.stream(ctx, sessionID, "snapshots", pad(to)+".batch")
}

func (remote *FileSystemSyncRemote) PutSnapshot(ctx context.Context, sessionID string, to Seq, body SyncBody) error {
	return remote.write(ctx, sessionID, []string{"snapshots"}, pad(to)+".batch", body)
}

func (remote *FileSystemSyncRemote) FoldBatches(ctx context.Context, sessionID string, afters []Seq) error {
	for _, after := range afters {
		slot, err := remote.slot(ctx, sessionID, after)
		if err != nil {
			return err
		}
		if slot == nil || slot.Folded {
			continue
		}
		folded := *slot
		folded.Folded = true
		if err := remote.writeSlot(ctx, sessionID, folded); err != nil {
			return err
		}
		body, err := remote.path(ctx, sessionID, "batches", pad(after)+".batch")
		if err != nil {
			return err
		}
		if err := remote.fs.Remove(ctx, body, false, true); err != nil {
			return err
		}
	}
	return nil
}

func (remote *FileSystemSyncRemote) DeleteSnapshots(ctx context.Context, sessionID string, snapshots []Seq) error {
	for _, to := range snapshots {
		path, err := remote.path(ctx, sessionID, "snapshots", pad(to)+".batch")
		if err != nil {
			return err
		}
		if err := remote.fs.Remove(ctx, path, false, true); err != nil {
			return err
		}
	}
	return nil
}

func (remote *FileSystemSyncRemote) List(ctx context.Context, sessionID string) (RemoteListing, error) {
	listing := RemoteListing{Batches: []BatchInfo{}, Snapshots: []Seq{}}
	names, err := remote.names(ctx, sessionID, "slots")
	if err != nil {
		return RemoteListing{}, err
	}
	for _, name := range names {
		match := slotName.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		after, _ := strconv.ParseInt(match[1], 10, 64)
		slot, err := remote.slot(ctx, sessionID, after)
		if err != nil {
			return RemoteListing{}, err
		}
		if slot != nil {
			listing.Batches = append(listing.Batches, *slot)
		}
	}
	names, err = remote.names(ctx, sessionID, "snapshots")
	if err != nil {
		return RemoteListing{}, err
	}
	for _, name := range names {
		if match := snapshotName.FindStringSubmatch(name); match != nil {
			to, _ := strconv.ParseInt(match[1], 10, 64)
			listing.Snapshots = append(listing.Snapshots, to)
		}
	}
	return listing, nil
}

// Sessions is every session, every time: a directory keeps no change order.
func (remote *FileSystemSyncRemote) Sessions(ctx context.Context, _ *string) (SessionChanges, error) {
	entries, err := remote.fs.ListDir(ctx, remote.root)
	if err != nil && !IsNotFound(err) {
		return SessionChanges{}, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.Kind == SyncDirectory && sessionIDPat.MatchString(entry.Name) {
			ids = append(ids, entry.Name)
		}
	}
	slices.Sort(ids)
	sessions := make([]SessionSummary, 0, len(ids))
	for _, id := range ids {
		listing, err := remote.List(ctx, id)
		if err != nil {
			return SessionChanges{}, err
		}
		meta, err := remote.GetMeta(ctx, id)
		if err != nil {
			return SessionChanges{}, err
		}
		sessions = append(sessions, SessionSummary{ID: id, Head: RemoteHead(listing), Meta: meta})
	}
	return SessionChanges{Sessions: sessions, Cursor: "", More: false}, nil
}

func (remote *FileSystemSyncRemote) PutMeta(ctx context.Context, sessionID string, meta *delta.JsonObject) error {
	text, err := jsstring.MarshalJSON(meta)
	if err != nil {
		return err
	}
	return remote.write(ctx, sessionID, nil, "meta.json", once(text))
}

func (remote *FileSystemSyncRemote) GetMeta(ctx context.Context, sessionID string) (*delta.JsonObject, error) {
	bytes, err := remote.read(ctx, sessionID, "meta.json")
	if err != nil || bytes == nil {
		return nil, err
	}
	value, err := delta.DecodeJson(bytes)
	if err != nil {
		return nil, fmt.Errorf("sync: the description of session %s: %w", sessionID, err)
	}
	meta, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, fmt.Errorf("sync: the description of session %s is not an object", sessionID)
	}
	return meta, nil
}

// slot is the slot starting at after; nil when none.
func (remote *FileSystemSyncRemote) slot(ctx context.Context, sessionID string, after Seq) (*BatchInfo, error) {
	content, err := remote.read(ctx, sessionID, "slots", pad(after)+".json")
	if err != nil || content == nil {
		return nil, err
	}
	var file slotFile
	if err := json.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("sync: slot %s of session %s: %w", pad(after), sessionID, err)
	}
	return &BatchInfo{After: file.After, To: file.To, Device: file.Device, Archive: file.Archive, Folded: file.Folded}, nil
}

func (remote *FileSystemSyncRemote) writeSlot(ctx context.Context, sessionID string, slot BatchInfo) error {
	text, err := json.Marshal(slotFile{After: slot.After, To: slot.To, Device: slot.Device, Archive: slot.Archive, Folded: slot.Folded})
	if err != nil {
		return err
	}
	return remote.write(ctx, sessionID, []string{"slots"}, pad(slot.After)+".json", once(text))
}

// names are the names in the directory of session's parts; none when it is missing.
func (remote *FileSystemSyncRemote) names(ctx context.Context, sessionID string, parts ...string) ([]string, error) {
	path, err := remote.path(ctx, sessionID, parts...)
	if err != nil {
		return nil, err
	}
	entries, err := remote.fs.ListDir(ctx, path)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name
	}
	return names, nil
}

// read is a file's bytes; nil when it is missing.
func (remote *FileSystemSyncRemote) read(ctx context.Context, sessionID string, parts ...string) ([]byte, error) {
	path, err := remote.path(ctx, sessionID, parts...)
	if err != nil {
		return nil, err
	}
	content, err := remote.fs.ReadBinaryFile(ctx, path)
	if IsNotFound(err) {
		return nil, nil
	}
	return content, err
}

// stream is a body streamed back from its file, a chunk at a time; nil when the file is missing.
func (remote *FileSystemSyncRemote) stream(ctx context.Context, sessionID string, parts ...string) (SyncBody, error) {
	path, err := remote.path(ctx, sessionID, parts...)
	if err != nil {
		return nil, err
	}
	reader, err := remote.fs.OpenBinaryReader(ctx, path)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &fileBody{reader: reader}, nil
}

// fileBody is a file as a body: chunks read at growing offsets, the file closed at the end or when the body is.
type fileBody struct {
	reader SyncFileReader
	offset int64
	closed bool
}

func (body *fileBody) Next(ctx context.Context) ([]byte, bool, error) {
	if body.closed {
		return nil, false, nil
	}
	chunk, err := body.reader.Read(ctx, body.offset, readChunk)
	if err != nil {
		return nil, false, errors.Join(err, body.Close())
	}
	if len(chunk) == 0 {
		return nil, false, body.Close()
	}
	body.offset += int64(len(chunk))
	return chunk, true, nil
}

func (body *fileBody) Close() error {
	if body.closed {
		return nil
	}
	body.closed = true
	//portlint:allow ctxbackground Pi's reader.close(context) runs after the body's own context may be aborted: the close must still release the file
	return body.reader.Close(context.Background())
}

// once is bytes as a one-chunk body.
func once(content []byte) SyncBody { return &oneChunk{content: content} }

type oneChunk struct {
	content []byte
	read    bool
}

func (body *oneChunk) Next(context.Context) ([]byte, bool, error) {
	if body.read {
		return nil, false, nil
	}
	body.read = true
	return body.content, true, nil
}

func (*oneChunk) Close() error { return nil }

// write writes a body through a temporary file and a rename, so a reader never sees half a file; the body is appended
// chunk by chunk as it streams, and closed.
func (remote *FileSystemSyncRemote) write(ctx context.Context, sessionID string, directory []string, name string, body SyncBody) (err error) {
	defer func() { err = errors.Join(err, body.Close()) }()
	dir, err := remote.path(ctx, sessionID, directory...)
	if err != nil {
		return err
	}
	if err := remote.fs.CreateDir(ctx, dir, true); err != nil {
		return err
	}
	path, err := remote.path(ctx, sessionID, append(slices.Clone(directory), name)...)
	if err != nil {
		return err
	}
	temporary, err := remote.path(ctx, sessionID, append(slices.Clone(directory), name+".tmp")...)
	if err != nil {
		return err
	}
	if err := remote.fs.WriteFile(ctx, temporary, nil); err != nil {
		return err
	}
	for {
		chunk, ok, err := body.Next(ctx)
		if err == nil && ok {
			err = remote.fs.AppendFile(ctx, temporary, chunk)
		}
		if err != nil {
			return errors.Join(err, remote.fs.Remove(context.WithoutCancel(ctx), temporary, false, true))
		}
		if !ok {
			break
		}
	}
	return remote.fs.RenameFile(ctx, temporary, path)
}

func (remote *FileSystemSyncRemote) path(ctx context.Context, sessionID string, parts ...string) (string, error) {
	checked, err := CheckSessionID(sessionID)
	if err != nil {
		return "", err
	}
	return remote.fs.JoinPath(ctx, append([]string{remote.root, checked}, parts...))
}
