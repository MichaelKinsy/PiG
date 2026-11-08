package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/node/app/tar.ts
//
// A minimal ustar (POSIX tar) writer and reader for the kit's address archives (slice 13 §4): regular files and directories,
// paths of at most 255 bytes, nothing else. The reader is a trust boundary: an archive may come from anywhere, so it refuses
// every entry that is no regular file or directory, a path that is absolute, holds `..`, a backslash or NUL, or names an
// empty segment, and a header whose checksum does not hold.
//
// Go forced differences (D455): an async iterable of chunks is an io.Reader, and a generator of entries a push or a pull:
// WriteTar takes an iter.Seq of entries and writes to an io.Writer; ReadTar calls visit with each entry (a file's Data is a
// reader to consume within the call; what it leaves unread is skipped after it returns).

// tarBlock is a tar block.
const tarBlock = 512

// TarEntry is one entry to write: its path (`/`-separated, relative) and its bytes, as a size and the reader that makes it up.
type TarEntry struct {
	Path string
	Size int64
	Data io.Reader
}

// TarKind is what a TarRead is.
type TarKind string

// The kinds of a TarRead.
const (
	TarFile      TarKind = "file"
	TarDirectory TarKind = "directory"
)

// TarRead is one entry read: a regular file (its Data to be consumed before ReadTar goes on) or a directory.
type TarRead struct {
	Kind TarKind
	Path string
	// Size is a file's size; 0 for a directory.
	Size int64
	// Data is a file's data; nil for a directory.
	Data io.Reader
}

// WriteTar writes the archive of entries to dst (two zero blocks end it).
func WriteTar(dst io.Writer, entries iter.Seq[TarEntry]) error {
	for entry := range entries {
		header, err := tarHeader(entry.Path, entry.Size)
		if err != nil {
			return err
		}
		if _, err := dst.Write(header); err != nil {
			return err
		}
		written, err := io.Copy(dst, io.LimitReader(entry.Data, entry.Size+1))
		if err != nil {
			return err
		}
		if written > entry.Size {
			return fmt.Errorf("tar: %s is larger than its size %d", entry.Path, entry.Size)
		}
		if written != entry.Size {
			return fmt.Errorf("tar: %s is %d bytes, not %d", entry.Path, written, entry.Size)
		}
		if pad := (tarBlock - entry.Size%tarBlock) % tarBlock; pad > 0 {
			if _, err := dst.Write(make([]byte, pad)); err != nil {
				return err
			}
		}
	}
	_, err := dst.Write(make([]byte, tarBlock*2))
	return err
}

// tarHeader is the ustar header of a regular file at path of size bytes.
func tarHeader(path string, size int64) ([]byte, error) {
	if err := checkTarPath(path); err != nil {
		return nil, err
	}
	block := make([]byte, tarBlock)
	name, prefix := []byte(path), []byte(nil)
	if len(name) > 100 {
		// Split at a `/` so the name has at most 100 bytes and the prefix at most 155.
		at := strings.LastIndexByte(path, '/')
		if at < 0 || at > 155 || len(path)-at-1 > 100 {
			return nil, fmt.Errorf("tar: the path %s is too long", path)
		}
		prefix, name = []byte(path[:at]), []byte(path[at+1:])
	}
	copy(block[0:], name)
	for _, field := range []struct {
		offset, length int
		text           string
	}{
		{100, 8, "0000644"}, {108, 8, "0000000"}, {116, 8, "0000000"},
		{124, 12, leftPad(strconv.FormatInt(size, 8), 11)},
		{136, 12, leftPad(strconv.FormatInt(time.Now().Unix(), 8), 11)},
	} {
		if err := tarField(block, field.offset, field.length, field.text); err != nil {
			return nil, err
		}
	}
	for i := 148; i < 156; i++ {
		block[i] = 0x20
	}
	block[156] = 0x30
	copy(block[257:], "ustar\x0000")
	copy(block[345:], prefix)
	sum := 0
	for _, b := range block {
		sum += int(b)
	}
	if err := tarField(block, 148, 8, leftPad(strconv.FormatInt(int64(sum), 8), 6)+"\x00 "); err != nil {
		return nil, err
	}
	return block, nil
}

// leftPad is text (ASCII digits) padded with zeros to width: Pi's `padStart(width, "0")`.
func leftPad(text string, width int) string { return fmt.Sprintf("%0*s", width, text) }

// parseOctal is `Number.parseInt(text, 8)`: an optional sign and the octal digits it starts with; false (NaN) when there are none.
func parseOctal(text string) (int64, bool) {
	negative := false
	if text != "" && (text[0] == '+' || text[0] == '-') {
		negative = text[0] == '-'
		text = text[1:]
	}
	var value int64
	digits := 0
	for ; digits < len(text) && text[digits] >= '0' && text[digits] <= '7'; digits++ {
		value = value*8 + int64(text[digits]-'0')
	}
	if digits == 0 {
		return 0, false
	}
	if negative {
		value = -value
	}
	return value, true
}

// tarField writes text (ASCII) at offset of block, NUL-terminated within length.
func tarField(block []byte, offset, length int, text string) error {
	if len(text) > length {
		return fmt.Errorf("tar: %s does not fit %d bytes", text, length)
	}
	copy(block[offset:], text)
	return nil
}

// checkTarPath refuses a path an archive may not hold: absolute, with `..`, `.`, an empty segment, a backslash or a NUL.
func checkTarPath(path string) error {
	bad := path == "" || strings.ContainsAny(path, "\\\x00") || strings.HasPrefix(path, "/")
	if !bad {
		for _, segment := range strings.Split(path, "/") {
			if segment == "" || segment == "." || segment == ".." {
				bad = true
				break
			}
		}
	}
	if bad {
		quoted, _ := chordjson.Marshal(path)
		return fmt.Errorf("tar: the path %s is refused", quoted)
	}
	return nil
}

// ReadTar reads the archive src entry by entry, calling visit with each (a file's data read within it; what visit leaves
// unread is skipped), refusing what it may not hold (see the file's header). It returns visit's first error.
func ReadTar(src io.Reader, visit func(entry TarRead) error) error {
	reader := &byteReader{source: src}
	for {
		block, err := reader.read(tarBlock)
		if err != nil {
			return err
		}
		if len(block) < tarBlock {
			return errors.New("tar: the archive ends inside a header")
		}
		if allZero(block) {
			return nil
		}
		path, size, directory, err := parseTarHeader(block)
		if err != nil {
			return err
		}
		padded := (size + tarBlock - 1) &^ (tarBlock - 1)
		if directory {
			if err := visit(TarRead{Kind: TarDirectory, Path: path}); err != nil {
				return err
			}
			if err := reader.skip(padded); err != nil {
				return err
			}
			continue
		}
		data := &tarData{reader: reader, left: size, path: path}
		if err := visit(TarRead{Kind: TarFile, Path: path, Size: size, Data: data}); err != nil {
			return err
		}
		if data.err != nil {
			return data.err
		}
		// What the consumer left unread, then the padding.
		if err := reader.skip(data.left + padded - size); err != nil {
			return err
		}
	}
}

func allZero(block []byte) bool {
	for _, b := range block {
		if b != 0 {
			return false
		}
	}
	return true
}

// tarData is a file's data within its entry.
type tarData struct {
	reader *byteReader
	left   int64
	path   string
	err    error
}

func (data *tarData) Read(p []byte) (int, error) {
	if data.left == 0 {
		return 0, io.EOF
	}
	want := min(int64(len(p)), data.left, 64*1024)
	chunk, err := data.reader.read(int(want))
	if err != nil {
		data.err = err
		return 0, err
	}
	if len(chunk) == 0 {
		data.err = fmt.Errorf("tar: the archive ends inside %s", data.path)
		return 0, data.err
	}
	data.left -= int64(len(chunk))
	return copy(p, chunk), nil
}

// parseTarHeader is a header's path, size and type, its checksum checked; anything but a regular file or a directory refused.
func parseTarHeader(block []byte) (path string, size int64, directory bool, err error) {
	stored, storedOK := parseOctal(strings.TrimSpace(tarText(block, 148, 8)))
	sum := 0
	for index := range tarBlock {
		if index >= 148 && index < 156 {
			sum += 0x20
		} else {
			sum += int(block[index])
		}
	}
	if !storedOK || stored != int64(sum) {
		return "", 0, false, errors.New("tar: a header's checksum does not hold")
	}
	flag := block[156]
	switch flag {
	case '0', 0:
	case '5':
		directory = true
	default:
		quoted, _ := chordjson.Marshal(string(rune(flag)))
		return "", 0, false, fmt.Errorf("tar: an entry of type %s is refused (files only)", quoted)
	}
	prefix, name := tarText(block, 345, 155), tarText(block, 0, 100)
	path = name
	if prefix != "" {
		path = prefix + "/" + name
	}
	path = strings.TrimSuffix(path, "/")
	if err := checkTarPath(path); err != nil {
		return "", 0, false, err
	}
	stamp := strings.TrimSpace(tarText(block, 124, 12))
	if stamp == "" {
		stamp = "0"
	}
	var sizeOK bool
	size, sizeOK = parseOctal(stamp)
	if !sizeOK || size < 0 || size > 1<<53-1 {
		return "", 0, false, fmt.Errorf("tar: %s has no valid size", path)
	}
	if directory {
		size = 0
	}
	return path, size, directory, nil
}

// tarText is the NUL-terminated text of a header field.
func tarText(block []byte, offset, length int) string {
	field := block[offset : offset+length]
	if end := bytes.IndexByte(field, 0); end >= 0 {
		field = field[:end]
	}
	if !utf8.Valid(field) {
		return strings.ToValidUTF8(string(field), "\uFFFD")
	}
	return string(field)
}

// byteReader reads exactly as many bytes as asked from a stream (fewer only at its end).
type byteReader struct {
	source io.Reader
	buffer []byte
}

// read is up to length bytes: length unless the stream ends first.
func (reader *byteReader) read(length int) ([]byte, error) {
	for len(reader.buffer) < length {
		chunk := make([]byte, max(length-len(reader.buffer), 32*1024))
		n, err := reader.source.Read(chunk)
		reader.buffer = append(reader.buffer, chunk[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	taken := reader.buffer[:min(length, len(reader.buffer))]
	reader.buffer = reader.buffer[len(taken):]
	return taken, nil
}

// skip skips length bytes.
func (reader *byteReader) skip(length int64) error {
	for left := length; left > 0; {
		skipped, err := reader.read(int(min(left, 64*1024)))
		if err != nil {
			return err
		}
		if len(skipped) == 0 {
			return errors.New("tar: the archive ends early")
		}
		left -= int64(len(skipped))
	}
	return nil
}
