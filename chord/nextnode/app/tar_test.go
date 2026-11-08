package app

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

// Ports packages/chord/test/node/app/tar.test.ts (Pi main c23b9ec1): tests of the kit's tar writer and reader (slice 13 §4):
// a round trip (long paths, empty and multi-block files, data left unread), and the reader refusing what an address archive
// may not hold: paths leaving the target (absolute, `..`), symbolic and hard links, devices, a bad checksum, a truncated
// archive.

func tarEntryOf(path, text string) TarEntry {
	return TarEntry{Path: path, Size: int64(len(text)), Data: strings.NewReader(text)}
}

func archiveOf(t *testing.T, entries ...TarEntry) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	err := WriteTar(&out, slices.Values(entries))
	return out.Bytes(), err
}

func mustArchive(t *testing.T, entries ...TarEntry) []byte {
	t.Helper()
	archive, err := archiveOf(t, entries...)
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

type read struct{ path, text string }

// entriesOf is every entry of archive: path and text (files) or `dir`; skip names files whose data is left unread.
func entriesOf(archive []byte, skip ...string) ([]read, error) {
	var got []read
	err := ReadTar(bytes.NewReader(archive), func(entry TarRead) error {
		switch {
		case entry.Kind == TarDirectory:
			got = append(got, read{entry.Path, "dir"})
		case slices.Contains(skip, entry.Path):
			got = append(got, read{entry.Path, "skipped"})
		default:
			text, err := io.ReadAll(entry.Data)
			if err != nil {
				return err
			}
			got = append(got, read{entry.Path, string(text)})
		}
		return nil
	})
	return got, err
}

// patched is a single-entry archive with header field offset set to value, its checksum recomputed.
func patched(t *testing.T, offset int, value string) []byte {
	t.Helper()
	archive := mustArchive(t, tarEntryOf("files/a", "x"))
	block := archive[:512]
	width := 1
	if offset == 0 {
		width = 100
	}
	clear(block[offset : offset+width])
	copy(block[offset:], value)
	for i := 148; i < 156; i++ {
		block[i] = 0x20
	}
	sum := 0
	for _, b := range block {
		sum += int(b)
	}
	copy(block[148:], leftPad(formatOctal(sum), 6)+"\x00 ")
	return archive
}

func formatOctal(n int) string {
	text := ""
	for ; n > 0; n /= 8 {
		text = string(rune('0'+n%8)) + text
	}
	if text == "" {
		return "0"
	}
	return text
}

func expectError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %v, want one containing %q", err, contains)
	}
}

func TestTarReadsBackWhatItWroteLongPathsEmptyAndMultiBlockFilesDataLeftUnread(t *testing.T) {
	long := "files/" + strings.Repeat("d", 90) + "/" + strings.Repeat("f", 90)
	big := strings.Repeat("y", 1500)
	archive := mustArchive(t, tarEntryOf("manifest.json", "{}"), tarEntryOf(long, "long"), tarEntryOf("files/empty", ""),
		tarEntryOf("files/big", big), tarEntryOf("files/last", "z"))
	if len(archive)%512 != 0 {
		t.Fatalf("archive is %d bytes", len(archive))
	}
	got, err := entriesOf(archive, "files/big")
	if err != nil {
		t.Fatal(err)
	}
	want := []read{{"manifest.json", "{}"}, {long, "long"}, {"files/empty", ""}, {"files/big", "skipped"}, {"files/last", "z"}}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v", got)
	}
}

func TestTarRefusesToWriteAPathItWouldRefuseToRead(t *testing.T) {
	for _, path := range []string{"/abs", "a/../b", "a//b", "./a", "a\\b"} {
		_, err := archiveOf(t, tarEntryOf(path, "x"))
		expectError(t, err, "is refused")
	}
}

func TestTarRefusesAbsolutePathsAndDotDotWhenReading(t *testing.T) {
	for _, path := range []string{"/etc/passwd", "files/../../evil", "../evil"} {
		_, err := entriesOf(patched(t, 0, path))
		expectError(t, err, "is refused")
	}
}

func TestTarRefusesSymbolicLinksHardLinksDevicesAndFifos(t *testing.T) {
	for _, flag := range []string{"1", "2", "3", "4", "6"} {
		_, err := entriesOf(patched(t, 156, flag))
		expectError(t, err, "is refused (files only)")
	}
}

func TestTarRefusesAHeaderWhoseChecksumDoesNotHoldAndATruncatedArchive(t *testing.T) {
	tampered := mustArchive(t, tarEntryOf("files/a", "x"))
	tampered[6] = 'b'
	_, err := entriesOf(tampered)
	expectError(t, err, "checksum")
	archive := mustArchive(t, tarEntryOf("files/a", strings.Repeat("x", 600)))
	_, err = entriesOf(archive[:700])
	expectError(t, err, "ends")
	_, err = entriesOf(archive[:100])
	expectError(t, err, "ends inside a header")
}

func TestTarRefusesAnEntryWhoseDataIsNotItsSize(t *testing.T) {
	_, err := archiveOf(t, TarEntry{Path: "a", Size: 5, Data: strings.NewReader("abc")})
	expectError(t, err, "is 3 bytes, not 5")
	_, err = archiveOf(t, TarEntry{Path: "a", Size: 2, Data: strings.NewReader("abc")})
	expectError(t, err, "is larger than its size 2")
	if errors.Is(err, io.EOF) {
		t.Fatal("a size error is no EOF")
	}
}
