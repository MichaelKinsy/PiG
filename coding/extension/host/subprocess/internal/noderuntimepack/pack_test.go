package noderuntimepack

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
)

func sampleFiles() []File {
	random := rand.New(rand.NewPCG(1, 2))
	noise := func(n int) []byte {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(random.UintN(256))
		}
		return data
	}
	return []File{
		{"runtime-node/cell.mjs", []byte("export const cell = 1\n")},
		{"runtime-node/empty.mjs", nil},
		{"runtime-node/shims/a/index.js", noise(3 << 20)},
		{"runtime-node/shims/a/lib/deep/very/long/" + string(bytes.Repeat([]byte("n"), 150)) + ".js", []byte("long name")},
		{"runtime-node/shims/pi-dist/pi-ai/index.js", noise(3 << 20)},
		{"runtime-node/shims/pi-dist/pi-ai/x.js", []byte("x")},
		{"runtime-node/shims/pi-dist/pi-tui/index.js", []byte("tui")},
	}
}

func extractAll(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	var mu sync.Mutex
	got := map[string][]byte{}
	if err := Extract(string(archive), func(name string, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		got[name] = bytes.Clone(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

// Every file comes back under its own name with its own bytes, including empty files and names longer than a tar header's name field.
func TestPackRoundTripsEveryFile(t *testing.T) {
	files := sampleFiles()
	archive, err := Pack(files)
	if err != nil {
		t.Fatal(err)
	}
	got := extractAll(t, archive)
	if len(got) != len(files) {
		t.Fatalf("extracted %d files, want %d", len(got), len(files))
	}
	for _, file := range files {
		if data, ok := got[file.Name]; !ok || !bytes.Equal(data, file.Data) {
			t.Errorf("%s: extracted %d bytes (present %v), want %d", file.Name, len(data), ok, len(file.Data))
		}
	}
}

// The generated archive is committed, so the same files must always give the same bytes.
func TestPackIsDeterministic(t *testing.T) {
	first, err := Pack(sampleFiles())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Pack(sampleFiles())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two packs of the same files differ")
	}
}

// Files that repeat one another land in one block, which is the point of the format: a copy costs almost nothing. Splitting a package directory across blocks would store the repeat twice.
func TestPackStoresARepeatedFileAlmostForFree(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 4))
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(random.UintN(256))
	}
	one, err := Pack([]File{{"runtime-node/shims/p/a.js", data}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := Pack([]File{{"runtime-node/shims/p/a.js", data}, {"runtime-node/shims/p/b.js", data}})
	if err != nil {
		t.Fatal(err)
	}
	if extra := len(two) - len(one); extra > len(one)/50 {
		t.Fatalf("a second identical 1 MiB file cost %d bytes on top of %d", extra, len(one))
	}
}

func TestGroupKeyKeepsAPackageTogether(t *testing.T) {
	for name, want := range map[string]string{
		"runtime-node/cell.mjs":                         "runtime-node",
		"runtime-node/harness/a.mjs":                    "runtime-node/harness",
		"runtime-node/shims/marked/lib/marked.js":       "runtime-node/shims/marked",
		"runtime-node/shims/runtime.mjs":                "runtime-node/shims",
		"runtime-node/shims/pi-dist/pi-ai/api/x.js":     "runtime-node/shims/pi-dist/pi-ai",
		"runtime-node/shims/pi-dist/pi-ai/sdk-bundle/y": "runtime-node/shims/pi-dist/pi-ai",
		"runtime-node/shims/pi-dist/package.json":       "runtime-node/shims/pi-dist",
	} {
		if got := groupKey(name); got != want {
			t.Errorf("groupKey(%q) = %q, want %q", name, got, want)
		}
	}
}

// A block ends only between package directories: a directory that straddles the size target stays whole.
func TestPackNeverSplitsAPackageAcrossBlocks(t *testing.T) {
	var files []File
	for i := range 6 {
		files = append(files, File{fmt.Sprintf("runtime-node/shims/big/f%d.js", i), bytes.Repeat([]byte{byte(i)}, 1<<20)})
	}
	files = append(files, File{"runtime-node/shims/next/a.js", []byte("a")})
	archive, err := Pack(files)
	if err != nil {
		t.Fatal(err)
	}
	blocks := 0
	for rest := string(archive); len(rest) > 0; blocks++ {
		length, n := uvarint(rest)
		rest = rest[n+int(length):]
	}
	if blocks != 2 {
		t.Fatalf("%d blocks, want the 6 MiB package in one and the next package in another", blocks)
	}
}

func TestExtractRejectsDamagedArchives(t *testing.T) {
	archive, err := Pack(sampleFiles())
	if err != nil {
		t.Fatal(err)
	}
	noop := func(string, []byte) error { return nil }
	if err := Extract(string(archive[:len(archive)-1]), noop); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated archive: %v, want ErrTruncated", err)
	}
	if err := Extract("\xff", noop); !errors.Is(err, ErrTruncated) {
		t.Fatalf("bad length: %v, want ErrTruncated", err)
	}
	damaged := bytes.Clone(archive)
	damaged[len(damaged)/2] ^= 0xff
	if err := Extract(string(damaged), noop); err == nil {
		t.Fatal("a flipped byte was not reported")
	}
}

func TestExtractStopsOnAVisitorError(t *testing.T) {
	archive, err := Pack(sampleFiles())
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	if err := Extract(string(archive), func(string, []byte) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("Extract = %v, want the visitor's error", err)
	}
}

func TestExtractRejectsNamesThatEscape(t *testing.T) {
	archive, err := Pack([]File{{"runtime-node/../escape", []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Extract(string(archive), func(string, []byte) error { return nil }); err == nil {
		t.Fatal("a name with .. was extracted")
	}
}
