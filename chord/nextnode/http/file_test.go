package http

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Tests of file responses (slice 10 §2): the type by extension (no active types), a `root` refusing a file whose real path
// lies outside it, and the file handle opened by the first read and closed when the request's scope seals, on every path (a
// full read, `HEAD`, a 416, a wrap replacing the response, a disconnect). Ports
// packages/chord/test/next-node/http/file.test.ts.

// fileOf is a file of size bytes named name in a fresh directory.
func fileOf(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, bytes.Repeat([]byte{97}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// openedOn is how many descriptors of this process are open on path (Linux).
func openedOn(path string) int {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		target, err := os.Readlink("/proc/self/fd/" + entry.Name())
		if err != nil {
			// Closed meanwhile.
			continue
		}
		if target == real {
			count++
		}
	}
	return count
}

func asHttpFailure(t *testing.T, err error, status int) {
	t.Helper()
	var failure *HttpFailure
	if !errors.As(err, &failure) || failure.Status != status {
		t.Fatalf("err = %v, want an HttpFailure %d", err, status)
	}
}

// Fable's recheck N1: a missing file was a reported 500, one report per request from anyone.
func TestFileResponseAnswersAMissingFile404WithOrWithoutRoot(t *testing.T) {
	dir := filepath.Dir(fileOf(t, "a.txt", 1))
	for _, options := range []FileResponseOptions{{}, {Root: dir}} {
		for _, name := range []string{"missing.txt", "a.txt/below"} {
			_, err := FileResponse(filepath.Join(dir, name), options)
			asHttpFailure(t, err, 404)
		}
	}
}

// Fable's security audit M1: a symbolic link inside a served directory reached any file.
func TestFileResponseWithARootRefusesAFileWhoseRealPathLiesOutsideIt403ServesOneInside(t *testing.T) {
	secret := fileOf(t, "secret.txt", 3)
	root := filepath.Join(filepath.Dir(secret), "pub")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	inside, err := FileResponse(filepath.Join(root, "a.txt"), FileResponseOptions{Root: root})
	if err != nil || inside.Size == nil || *inside.Size != 1 {
		t.Fatalf("inside = %+v, %v", inside, err)
	}
	for _, name := range []string{"link.txt", "../secret.txt"} {
		_, err := FileResponse(filepath.Join(root, name), FileResponseOptions{Root: root})
		asHttpFailure(t, err, 403)
	}
	trusted, err := FileResponse(filepath.Join(root, "link.txt"), FileResponseOptions{})
	if err != nil || trusted.Size == nil || *trusted.Size != 3 {
		t.Fatalf("trusted = %+v, %v", trusted, err)
	}
}

func TestFileResponseTypesByExtensionTextImagesAudioVideoNeverHTMLOrSVGUnlessNamed(t *testing.T) {
	names := []string{"a.txt", "a.png", "a.mp3", "a.webm", "a.html", "a.svg", "a.bin"}
	want := []string{
		"text/plain; charset=utf-8", "image/png", "audio/mpeg", "video/webm",
		"application/octet-stream", "application/octet-stream", "application/octet-stream",
	}
	for i, name := range names {
		response, err := FileResponse(fileOf(t, name, 1), FileResponseOptions{})
		if err != nil || response.Kind != KindBytes || response.Mime != want[i] {
			t.Fatalf("%s: %+v, %v, want %s", name, response, err, want[i])
		}
	}
	named, err := FileResponse(fileOf(t, "b.svg", 1), FileResponseOptions{Mime: "image/svg+xml"})
	if err != nil || named.Mime != "image/svg+xml" {
		t.Fatalf("named = %+v, %v", named, err)
	}
	if _, err := FileResponse(filepath.Join(os.TempDir(), "chord-http-missing"), FileResponseOptions{}); err == nil {
		t.Fatal("a missing file has a response")
	}
}

func TestFileResponseClosesItsHandleOnEveryPathOpensNoneWhenNothingIsWritten(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptors are counted through /proc/self/fd")
	}
	path := fileOf(t, "big.bin", 50*1024*1024)
	server := serving(t, []plugin{{"@app/a", contributing(HttpContribution{
		Routes: []HttpRouteEntry{
			RouteFunc("GET /f", func(HttpRequest, host.Ctx) (HttpResponse, error) {
				return FileResponse(path, FileResponseOptions{Ranges: true, Download: "big.bin"})
			}),
			RouteFunc("GET /wrapped", func(HttpRequest, host.Ctx) (HttpResponse, error) {
				return FileResponse(path, FileResponseOptions{})
			}),
		},
		Wraps: []HttpWrap{{
			Name: "replace", Phase: PhaseTransform, Route: "GET /wrapped",
			Wrap: func(HttpRequest, func(HttpRequest) (HttpResponse, error), host.Ctx) (HttpResponse, error) {
				return HttpResponse{Kind: KindError, Status: 403}, nil
			},
		}},
	})}}, servedOptions{})
	port := server.port
	closed := func(what string) {
		t.Helper()
		eventually(t, what+": the handle closed", func() bool { return openedOn(path) == 0 })
	}
	full := mustSend(t, port, "GET", "/f", sendOptions{})
	if full.status != 200 || len(full.body) != 50*1024*1024 {
		t.Fatalf("full = %d %d", full.status, len(full.body))
	}
	closed("a full read")
	if got := mustSend(t, port, "HEAD", "/f", sendOptions{}).status; got != 200 || openedOn(path) != 0 {
		t.Fatalf("HEAD = %d, opened %d", got, openedOn(path))
	}
	past := mustSend(t, port, "GET", "/f", sendOptions{headers: map[string]string{"range": "bytes=999999999-"}})
	if past.status != 416 || openedOn(path) != 0 {
		t.Fatalf("416 = %d, opened %d", past.status, openedOn(path))
	}
	if got := mustSend(t, port, "GET", "/wrapped", sendOptions{}).status; got != 403 || openedOn(path) != 0 {
		t.Fatalf("wrapped = %d, opened %d", got, openedOn(path))
	}
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET /f HTTP/1.1\r\nHost: 127.0.0.1:" + strconv.Itoa(port) + "\r\n\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(within))
	if _, err := conn.Read(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if got := openedOn(path); got != 1 {
		t.Fatalf("opened while reading = %d, want 1", got)
	}
	conn.Close()
	closed("a disconnect")
}
