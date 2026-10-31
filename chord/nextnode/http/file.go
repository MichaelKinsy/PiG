package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/next-node/http/file.ts. File responses (slice 10 §2; decision "Slice 10 fix round, three small
// points" (1)): a file as a `bytes` response. `FileResponse` stats the file at once (with a Root, its real path must lie
// inside it); the file is opened by the first `read` and closed when the request's scope seals (its Done), so a response
// nobody writes (`HEAD`, a 416, a disconnect, a wrap that replaced it) opens nothing and leaks nothing.
//
// Forced Go differences. The options are a struct (zero values for Pi's absent ones: Download "", Root "", Mime ""); the
// file handle promise is a sync.Once; ENOENT and ENOTDIR are fs.ErrNotExist and syscall.ENOTDIR.

// FileResponseOptions is what `fileResponse` takes besides the path.
type FileResponseOptions struct {
	// Download is the name it is saved under.
	Download string
	// Inline is shown in the browser instead of saved (an image, a video), with a `sandbox` CSP.
	Inline bool
	// Root is the directory the file must lie in once its symbolic links are resolved: one outside it (a link out) is
	// refused 403. Without it the path is trusted (a plugin's own file).
	Root string
	// Ranges is whether a single range is answered 206 (audio and video seek with it).
	Ranges bool
	// Mime is the type; by default from the extension (text, images, audio, video), else `application/octet-stream`.
	Mime string
}

// FileResponse is the file at given as a `bytes` response; *HttpFailure 404 for a missing one (anyone may ask for one: no
// failure of the route), 403 for one outside Root; it returns what else stat returns.
func FileResponse(given string, options FileResponseOptions) (HttpResponse, error) {
	path := given
	if options.Root != "" {
		real, err := inside(given, options.Root)
		if err != nil {
			return HttpResponse{}, missingAsNotFound(err)
		}
		path = real
	}
	info, err := os.Stat(path)
	if err != nil {
		return HttpResponse{}, missingAsNotFound(err)
	}
	size := info.Size()
	var once sync.Once
	var file *os.File
	var openErr error
	read := func(offset int64, length int, ctx host.Ctx) ([]byte, error) {
		once.Do(func() { file, openErr = opened(path, ctx) })
		if openErr != nil {
			return nil, openErr
		}
		// Only the bytes read are handed out.
		buffer := make([]byte, length)
		n, err := file.ReadAt(buffer, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return buffer[:n], nil
	}
	mime := options.Mime
	if mime == "" {
		mime = mimeOf(path)
	}
	return HttpResponse{
		Kind: KindBytes, Mime: mime, Size: &size, Read: read,
		Download: options.Download, Inline: options.Inline, Ranges: options.Ranges,
	}, nil
}

// missingAsNotFound is a missing file as the 404 failure it is; any other error as it is.
func missingAsNotFound(err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return NewHttpFailure(404, "no such file")
	}
	return err
}

// inside is the real path of path when it lies inside root's real path; *HttpFailure 403 otherwise.
func inside(path, root string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	prefix := base
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if real != base && !strings.HasPrefix(real, prefix) {
		return "", NewHttpFailure(403, "the file lies outside its root")
	}
	return real, nil
}

// opened is path opened, closed when ctx (the request's scope) ends; at once when it ended already.
func opened(path string, ctx context.Context) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if ctx.Done() == nil {
		file.Close()
		return nil, fmt.Errorf("fileResponse(%s): read outside a request's scope", filepath.Base(path))
	}
	context.AfterFunc(ctx, func() { file.Close() })
	return file, nil
}

var mimeTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".json": "application/json",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
}

// mimeOf is the type of path by its extension: text, images, audio and video only. Active types (HTML, SVG) never come from
// here: a route serving one names it (Mime).
func mimeOf(path string) string {
	if mime, ok := mimeTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return mime
	}
	return "application/octet-stream"
}
