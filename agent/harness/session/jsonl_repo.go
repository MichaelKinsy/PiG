package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// JsonlSessionRepo owns exclusive handles and discovery for format-4 sessions.
type JsonlSessionRepo struct {
	fs             harness.FileSystem
	rootInput      string
	now            func() int64
	mu             sync.Mutex
	openSessions   map[string]*JsonlStorage
	pendingCreates map[string]bool
	closed         bool
}

func NewJsonlSessionRepo(options JsonlSessionRepoOptions) *JsonlSessionRepo {
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	return &JsonlSessionRepo{fs: options.FileSystem, rootInput: options.SessionsRoot, now: now, openSessions: map[string]*JsonlStorage{}, pendingCreates: map[string]bool{}}
}

func metadataFromHeader(header JsonlStorageHeader, path string, modifiedAt int64) SessionMetadata {
	return SessionMetadata{ID: header.ID, CreatedAt: header.CreatedAt, StorageVersion: header.StorageVersion, Cwd: header.Cwd, Path: path, ModifiedAt: modifiedAt, ParentSessionID: header.ParentSessionID, LegacyParentSessionPath: header.LegacyParentSessionPath}
}

func sessionDirectoryName(cwd string) string {
	if strings.HasPrefix(cwd, "/") || strings.HasPrefix(cwd, `\`) {
		cwd = cwd[1:]
	}
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(cwd) + "--"
}

func encodeSessionID(id string) string {
	var out strings.Builder
	const hex = "0123456789ABCDEF"
	for _, value := range []byte(id) {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-_.!~*'()", rune(value)) {
			out.WriteByte(value)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[value>>4])
			out.WriteByte(hex[value&15])
		}
	}
	return out.String()
}

func sessionFileName(created int64, id string) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(time.UnixMilli(created).UTC().Format("2006-01-02T15:04:05.000Z")) + "_" + encodeSessionID(id) + ".jsonl"
}

func (repo *JsonlSessionRepo) assertOpen() error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.closed {
		return fmt.Errorf("JsonlSessionRepo is closed")
	}
	return nil
}
func sessionKey(cwd, id string) string { return cwd + "\x00" + id }

func (repo *JsonlSessionRepo) reserve(key, id string) error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.openSessions[key] != nil || repo.pendingCreates[key] {
		return fmt.Errorf("Session already exists: %s", id)
	}
	repo.pendingCreates[key] = true
	return nil
}
func (repo *JsonlSessionRepo) release(key string) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	delete(repo.pendingCreates, key)
}

func (repo *JsonlSessionRepo) root(ctx context.Context) (string, error) {
	root, err := repo.fs.AbsolutePath(ctx, repo.rootInput)
	if err != nil {
		return "", fmt.Errorf("Failed to resolve sessions root %s: %w", repo.rootInput, err)
	}
	return root, nil
}
func (repo *JsonlSessionRepo) sessionDirectory(ctx context.Context, cwd string) (string, error) {
	root, err := repo.root(ctx)
	if err != nil {
		return "", err
	}
	directory, err := repo.fs.JoinPath(ctx, []string{root, sessionDirectoryName(cwd)})
	if err != nil {
		return "", fmt.Errorf("Failed to resolve sessions directory for %s: %w", cwd, err)
	}
	return directory, nil
}

func (repo *JsonlSessionRepo) resolveNewSessionPath(ctx context.Context, cwd string, created int64, id string) (string, error) {
	directory, err := repo.sessionDirectory(ctx, cwd)
	if err != nil {
		return "", err
	}
	exists, err := repo.fs.Exists(ctx, directory)
	if err != nil {
		return "", fmt.Errorf("Failed to check sessions directory %s: %w", directory, err)
	}
	if exists {
		files, err := repo.fs.ListDir(ctx, directory)
		if err != nil {
			return "", fmt.Errorf("Failed to list sessions directory %s: %w", directory, err)
		}
		suffix := "_" + encodeSessionID(id) + ".jsonl"
		for _, file := range files {
			if file.Kind != "directory" && strings.HasSuffix(file.Name, suffix) {
				return "", fmt.Errorf("Session already exists: %s", id)
			}
		}
	}
	if err := repo.fs.CreateDir(ctx, directory, nil); err != nil {
		return "", fmt.Errorf("Failed to create sessions directory %s: %w", directory, err)
	}
	path, err := repo.fs.JoinPath(ctx, []string{directory, sessionFileName(created, id)})
	if err != nil {
		return "", fmt.Errorf("Failed to resolve path for session %s: %w", id, err)
	}
	return path, nil
}

func (repo *JsonlSessionRepo) publish(metadata SessionMetadata, storage *JsonlStorage, key string) (Session, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.openSessions[key] != nil {
		return nil, fmt.Errorf("Session is already open: %s", metadata.ID)
	}
	opened := NewStorageBackedSession(metadata, storage, &StorageBackedSessionOptions{OnClose: func() {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		if repo.openSessions[key] == storage {
			delete(repo.openSessions, key)
		}
	}})
	repo.openSessions[key] = storage
	return opened, nil
}

// Create publishes an empty session only after its complete header is durable.
func (repo *JsonlSessionRepo) Create(ctx context.Context, options SessionCreateOptions) (opened Session, err error) {
	if err = repo.assertOpen(); err != nil {
		return nil, err
	}
	created := repo.now()
	id := options.ID
	if id == "" {
		id, err = UUIDv7(&created)
		if err != nil {
			return nil, err
		}
	}
	cwd, err := repo.fs.AbsolutePath(ctx, options.Cwd)
	if err != nil {
		return nil, fmt.Errorf("Failed to resolve session cwd %s: %w", options.Cwd, err)
	}
	key := sessionKey(cwd, id)
	if err = repo.reserve(key, id); err != nil {
		return nil, err
	}
	defer repo.release(key)
	path, err := repo.resolveNewSessionPath(ctx, cwd, created, id)
	if err != nil {
		return nil, err
	}
	var storage *JsonlStorage
	defer func() {
		if err != nil {
			if storage != nil {
				_ = storage.Close(ctx)
			}
			_ = repo.fs.Remove(ctx, path, &harness.RemoveOptions{Force: true})
		}
	}()
	header := JsonlStorageHeader{V: JSONLFormatVersion, Kind: "header", ID: id, StorageVersion: JSONLStorageVersion, CreatedAt: created, Cwd: cwd, ParentSessionID: options.ParentSessionID}
	storage, err = CreateJsonlStorage(ctx, JsonlStorageOptions{FileSystem: repo.fs, Path: path, Now: repo.now}, header, nil)
	if err != nil {
		return nil, err
	}
	info, err := repo.fs.FileInfo(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read session %s: %w", path, err)
	}
	return repo.publish(metadataFromHeader(header, path, int64(info.MtimeMs)), storage, key)
}

// Open validates identity and storage version before granting an exclusive handle.
func (repo *JsonlSessionRepo) Open(ctx context.Context, metadata SessionMetadata) (Session, error) {
	if err := repo.assertOpen(); err != nil {
		return nil, err
	}
	key := sessionKey(metadata.Cwd, metadata.ID)
	repo.mu.Lock()
	alreadyOpen := repo.openSessions[key] != nil
	repo.mu.Unlock()
	if alreadyOpen {
		return nil, fmt.Errorf("Session is already open: %s", metadata.ID)
	}
	exists, err := repo.fs.Exists(ctx, metadata.Path)
	if err != nil {
		return nil, fmt.Errorf("Failed to check session %s: %w", metadata.Path, err)
	}
	if !exists {
		return nil, fmt.Errorf("Session file does not exist: %s", metadata.Path)
	}
	storage, err := OpenJsonlStorage(ctx, JsonlStorageOptions{FileSystem: repo.fs, Path: metadata.Path, Now: repo.now})
	if err != nil {
		return nil, err
	}
	if storage.Header.ID != metadata.ID || storage.Header.Cwd != metadata.Cwd {
		_ = storage.Close(ctx)
		return nil, fmt.Errorf("Session identity does not match header: %s", metadata.ID)
	}
	opened, err := repo.publish(metadata, storage, key)
	if err != nil {
		_ = storage.Close(ctx)
	}
	return opened, err
}

func (repo *JsonlSessionRepo) readMetadata(ctx context.Context, file harness.FileInfo) (*SessionMetadata, error) {
	lines, err := repo.fs.ReadTextLines(ctx, file.Path, &harness.ReadTextLinesOptions{MaxLines: new(1)})
	if err != nil {
		return nil, fmt.Errorf("Failed to read session header %s: %w", file.Path, err)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	parsed, err := ParseJsonlSessionHeader(lines[0])
	if err != nil {
		return nil, nil
	}
	header := parsed.Header
	if parsed.Legacy != nil {
		normalized, err := normalizeLegacyV3Header(ctx, repo.fs, *parsed.Legacy)
		if err != nil {
			return nil, err
		}
		header = &normalized
	}
	metadata := metadataFromHeader(*header, file.Path, int64(file.MtimeMs))
	return &metadata, nil
}

func (repo *JsonlSessionRepo) listDirectory(ctx context.Context, directory string, cwd *string) ([]SessionMetadata, error) {
	exists, err := repo.fs.Exists(ctx, directory)
	if err != nil {
		return nil, fmt.Errorf("Failed to check sessions directory %s: %w", directory, err)
	}
	if !exists {
		return []SessionMetadata{}, nil
	}
	files, err := repo.fs.ListDir(ctx, directory)
	if err != nil {
		return nil, fmt.Errorf("Failed to list sessions directory %s: %w", directory, err)
	}
	metadata := []SessionMetadata{}
	for _, file := range files {
		if file.Kind == "directory" || !strings.HasSuffix(file.Name, ".jsonl") {
			continue
		}
		found, err := repo.readMetadata(ctx, file)
		if err != nil {
			return nil, err
		}
		if found != nil && (cwd == nil || found.Cwd == *cwd) {
			metadata = append(metadata, *found)
		}
	}
	return metadata, nil
}

// List discovers valid headers newest first, optionally within one exact cwd.
func (repo *JsonlSessionRepo) List(ctx context.Context, options *JsonlSessionListOptions) ([]SessionMetadata, error) {
	if err := repo.assertOpen(); err != nil {
		return nil, err
	}
	var cwd *string
	if options != nil && options.Cwd != nil {
		resolved, err := repo.fs.AbsolutePath(ctx, *options.Cwd)
		if err != nil {
			return nil, fmt.Errorf("Failed to resolve session cwd %s: %w", *options.Cwd, err)
		}
		cwd = &resolved
	}
	root, err := repo.root(ctx)
	if err != nil {
		return nil, err
	}
	exists, err := repo.fs.Exists(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("Failed to check sessions root %s: %w", root, err)
	}
	if !exists {
		return []SessionMetadata{}, nil
	}
	directories := []string{}
	if cwd != nil {
		directory, err := repo.sessionDirectory(ctx, *cwd)
		if err != nil {
			return nil, err
		}
		directories = append(directories, directory)
	} else {
		files, err := repo.fs.ListDir(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("Failed to list sessions root %s: %w", root, err)
		}
		for _, file := range files {
			if file.Kind == "directory" {
				directories = append(directories, file.Path)
			}
		}
	}
	metadata := []SessionMetadata{}
	for _, directory := range directories {
		items, err := repo.listDirectory(ctx, directory, cwd)
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, items...)
	}
	comparer := collate.New(language.English)
	slices.SortFunc(metadata, func(left, right SessionMetadata) int {
		if left.CreatedAt > right.CreatedAt {
			return -1
		}
		if left.CreatedAt < right.CreatedAt {
			return 1
		}
		if order := comparer.CompareString(left.ID, right.ID); order != 0 {
			return order
		}
		return comparer.CompareString(left.Cwd, right.Cwd)
	})
	return metadata, nil
}

// Delete removes a closed session file.
func (repo *JsonlSessionRepo) Delete(ctx context.Context, metadata SessionMetadata) error {
	if err := repo.assertOpen(); err != nil {
		return err
	}
	repo.mu.Lock()
	open := repo.openSessions[sessionKey(metadata.Cwd, metadata.ID)] != nil
	repo.mu.Unlock()
	if open {
		return fmt.Errorf("Session is open: %s", metadata.ID)
	}
	exists, err := repo.fs.Exists(ctx, metadata.Path)
	if err != nil {
		return fmt.Errorf("Failed to check session %s: %w", metadata.Path, err)
	}
	if !exists {
		return fmt.Errorf("Session file does not exist: %s", metadata.Path)
	}
	if err := repo.fs.Remove(ctx, metadata.Path, nil); err != nil {
		return fmt.Errorf("Failed to delete session %s: %w", metadata.Path, err)
	}
	return nil
}

func (repo *JsonlSessionRepo) forkInput(ctx context.Context, metadata SessionMetadata) (jsonlForkInput, error) {
	input := jsonlForkInput{metadata: metadata}
	repo.mu.Lock()
	storage := repo.openSessions[sessionKey(metadata.Cwd, metadata.ID)]
	repo.mu.Unlock()
	if storage != nil {
		if storage.IsLegacyV3() {
			return input, fmt.Errorf("Cannot fork an open legacy v3 JSONL session; commit a non-empty transaction to upgrade it to format 4 first")
		}
		next, err := storage.CaptureForkNextSeq(ctx)
		input.nextSeq = &next
		return input, err
	}
	lines, err := repo.fs.ReadTextLines(ctx, metadata.Path, &harness.ReadTextLinesOptions{MaxLines: new(1)})
	if err != nil {
		return input, fmt.Errorf("Failed to read session header %s: %w", metadata.Path, err)
	}
	if len(lines) > 0 {
		parsed, err := ParseJsonlSessionHeader(lines[0])
		if err == nil && parsed.Legacy != nil {
			source, err := ReadLegacyV3Source(ctx, repo.fs, metadata.Path)
			if err != nil {
				return input, err
			}
			if source.Header.ID != metadata.ID || source.Header.Cwd != metadata.Cwd {
				return input, fmt.Errorf("Session identity does not match header: %s", metadata.ID)
			}
			input.legacy = source
		}
	}
	return input, nil
}

// Fork indexes source structure and streams only selected current state into a new file.
func (repo *JsonlSessionRepo) Fork(ctx context.Context, source SessionMetadata, options ForkOptions) (opened Session, err error) {
	if err = repo.assertOpen(); err != nil {
		return nil, err
	}
	created := repo.now()
	id := options.ID
	if id == "" {
		id, err = UUIDv7(&created)
		if err != nil {
			return nil, err
		}
	}
	key := sessionKey(source.Cwd, id)
	if err = repo.reserve(key, id); err != nil {
		return nil, err
	}
	defer repo.release(key)
	input, err := repo.forkInput(ctx, source)
	if err != nil {
		return nil, err
	}
	path, err := repo.resolveNewSessionPath(ctx, source.Cwd, created, id)
	if err != nil {
		return nil, err
	}
	var storage *JsonlStorage
	defer func() {
		if err != nil {
			if storage != nil {
				_ = storage.Close(ctx)
			}
			_ = repo.fs.Remove(ctx, path, &harness.RemoveOptions{Force: true})
		}
	}()
	header := JsonlStorageHeader{V: JSONLFormatVersion, Kind: "header", ID: id, StorageVersion: JSONLStorageVersion, CreatedAt: created, Cwd: source.Cwd, ParentSessionID: source.ID}
	if err = runJsonlFork(ctx, repo.fs, input, path, header, options); err != nil {
		return nil, err
	}
	storage, err = OpenJsonlStorage(ctx, JsonlStorageOptions{FileSystem: repo.fs, Path: path, Now: repo.now})
	if err != nil {
		return nil, err
	}
	info, err := repo.fs.FileInfo(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read session %s: %w", path, err)
	}
	return repo.publish(metadataFromHeader(header, path, int64(info.MtimeMs)), storage, key)
}

// Close seals repository admission; existing session handles own their own lifetime.
func (repo *JsonlSessionRepo) Close(context.Context) error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.closed = true
	return nil
}
