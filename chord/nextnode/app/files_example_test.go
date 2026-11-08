package app

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/examples/65-files.ts (decision "Files v2"): the kit's file storage, one `Files` object per
// host, its bytes in the host's `files/` and its index in the host's store; a space and a path, versions, a live
// `FilesFolder`, keys, queries, chunked writes, the summary of a space, and `copyFiles`. The script's printed lines are
// the assertions here.
func TestExample65Files(t *testing.T) {
	f := filesOf(t, fixtureOptions{Dir: filepath.Join(t.TempDir(), "host")})

	// 1. Writes: a space, a path (its folders are made), a type, who wrote it, a producer's reference, free `meta`.
	report := f.must("put", "space", "shared", "path", "docs/report.md", "mime", "text/markdown", "source", "owner", "ref", "message-7",
		"meta", delta.JsonObjectOf("title", "Q3"), "bytes", []byte("# Report")).(*delta.JsonObject)
	if str(report, "path") != "docs/report.md" || num(report, "version") != 1 || jsonOf(t, report.Value("meta")) != `{"title":"Q3"}` {
		t.Fatalf("1. put: %s", jsonOf(t, report))
	}

	// 2. A second write to the path is version 2; the path names the newest, version 1 stays readable.
	second := f.must("put", "space", "shared", "path", "docs/report.md", "mime", "text/markdown", "source", "agent", "bytes", []byte("# Report v2")).(*delta.JsonObject)
	newest := f.must("get", "space", "shared", "path", "docs/report.md").(*delta.JsonObject)
	if num(newest, "version") != 2 || f.text(str(second, "id")) != "# Report v2" || f.text(str(report, "id")) != "# Report" {
		t.Fatalf("2. versions: newest %v", newest.Value("version"))
	}

	// 3. Folders are listed folders first, then each file's newest version; a live listing follows changes.
	docs, err := f.host.Get(background, FilesFolder, delta.JsonObjectOf("space", "shared", "folder", "docs"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listed := func() []string {
		value, err := docs.Member("listing").Value()
		if err != nil {
			t.Fatal(err)
		}
		return entryNames(value.(*delta.JsonObject).Value("entries"), true)
	}
	equalNames(t, "3. docs", listed(), "docs/report.md")
	f.must("mkdir", "space", "shared", "path", "docs/drafts")
	f.must("move", "space", "shared", "from", "docs/report.md", "to", "docs/drafts/report.md")
	equalNames(t, "3. after mkdir and move", listed(), "docs/drafts/")

	// 4. A key: a retried delivery (Discord's message 42, attachment 0) stores nothing the second time.
	delivery := []any{"space", "discord", "path", "inbox/photo.png", "mime", "image/png", "source", "discord", "ref", "42", "key", "discord:42:0", "bytes", []byte("png bytes")}
	first := f.must("put", delivery...).(*delta.JsonObject)
	retried := f.must("put", delivery...).(*delta.JsonObject)
	if str(first, "id") != str(retried, "id") || num(f.summary("discord"), "versions") != 1 {
		t.Fatalf("4. retry stored again: %s", jsonOf(t, f.summary("discord")))
	}

	// 5. Queries: by `ref`, by `key`; a copy shares the bytes.
	byRef := f.must("byRef", "space", "discord", "ref", "42").(*delta.JsonObject)
	equalNames(t, "5. by ref 42", entryPaths(byRef.Value("items")), "inbox/photo.png")
	copied := f.must("copy", "id", str(first, "id"), "space", "shared", "path", "photo.png").(*delta.JsonObject)
	if f.text(str(copied, "id")) != f.text(str(first, "id")) {
		t.Fatal("5. the copy holds other bytes")
	}

	// 6. Chunked writes for large files: begin, append at the stored offset, finish.
	begun := f.must("begin", "space", "shared", "path", "big.bin", "mime", "application/octet-stream", "source", "owner").(*delta.JsonObject)
	chunk := bytes.Repeat([]byte{1}, 1024*1024)
	for offset := 0; offset < 3*len(chunk); offset += len(chunk) {
		f.must("append", "upload", str(begun, "upload"), "offset", float64(offset), "bytes", chunk)
	}
	if got := f.mustRaw("finish", str(begun, "upload")).(*delta.JsonObject); num(got, "size") != float64(3*len(chunk)) {
		t.Fatalf("6. chunked: %v bytes", got.Value("size"))
	}

	// 7. The summary of a space: what a worker pushes to its server's index.
	summary := f.summary("shared")
	if num(summary, "files") != 3 || num(summary, "versions") != 4 {
		t.Fatalf("7. shared: %s", jsonOf(t, summary))
	}

	// 8. Copying the host's files (index and bytes, nothing else of its store) into a new host's directory.
	root := filepath.Dir(f.dir)
	if err := CopyFiles(FilesSource{Store: f.store, Files: filepath.Join(f.dir, "files")}, filepath.Join(root, "copy"), background); err != nil {
		t.Fatal(err)
	}
	other := filesOf(t, fixtureOptions{Dir: filepath.Join(root, "copy")})
	if entries := other.must("list", "space", "shared", "folder", "docs/drafts").(*delta.JsonObject).Value("items").([]any); len(entries) != 1 {
		t.Fatalf("8. the copy's docs/drafts: %d entries", len(entries))
	}
	other.close()
	docs.Release()
	f.close()
}

// entryPaths are the paths of file versions.
func entryPaths(items any) []string {
	paths := []string{}
	for _, item := range items.([]any) {
		paths = append(paths, item.(*delta.JsonObject).Value("path").(string))
	}
	return paths
}
