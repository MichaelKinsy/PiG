// Package execfile writes a file that this process executes afterwards.
//
// Linux, macOS and the BSDs refuse to execute a file that any process holds open for writing: execve fails with ETXTBSY ("text file busy"). The refusal follows the open file, not the path, so renaming the file into place does not clear it. A Go program forks while its other goroutines run, and a child holds a copy of every descriptor that existed when it was forked until it calls exec: close-on-exec closes the copy at exec, not at fork. A child forked while this process had the file open therefore keeps the file busy after this process closed it, and the exec that follows fails (golang.org/issue/22315). The window is the child's time from fork to exec. A loaded host stretches it to milliseconds, so it opens wherever one goroutine writes a binary while others start extensions, builds, or tools.
//
// Write closes the window: no write descriptor of the file exists while this process can fork. Go forks under the write side of syscall.ForkLock, so Write opens the file, writes one chunk, and closes it while holding the lock for reading, and releases the lock between chunks. A child is forked only while no descriptor is open, so none holds one. The lock is held for one chunk at a time, so a process start waits for at most one chunk of the copy, not the whole file. Nothing retries.
package execfile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// chunkSize is how many bytes Write copies per hold of the fork lock: the longest a process start waits for a write.
var chunkSize int64 = 256 << 10

// Write creates or opens name with the extra flags in flag (os.O_CREATE, os.O_EXCL, os.O_TRUNC) and copies content into it, so that this process starts no child while a descriptor of the file is open. The umask masks perm, as in os.OpenFile; a caller that needs other bits calls os.Chmod with the name afterwards.
//
// The file is closed and opened again between chunks (the later opens seek to the end of the written part), so nothing else may remove or replace name until Write returns, and the file is visible with a partial length meanwhile. Every goroutine that starts a process waits while a chunk is written, so content must be memory or a file. A pipe, a socket, or a reader that waits for a goroutine that starts a process can deadlock.
func Write(name string, flag int, perm fs.FileMode, content io.Reader) error {
	// One limited reader and, for content that is not a file, one buffer serve every chunk.
	limited := &io.LimitedReader{R: content}
	remaining := remainingBytes(content)
	var buffer []byte
	switch content.(type) {
	case *os.File, *byteSource:
	default:
		buffer = make([]byte, 32<<10)
		if remaining < 0 || remaining > int64(len(buffer)) {
			buffer = make([]byte, min(chunkSize, 256<<10))
		}
	}
	for first, offset := true, int64(0); ; first = false {
		limited.N = chunkSize
		if remaining >= 0 {
			limited.N = min(chunkSize, remaining)
		}
		want := limited.N
		if err := writeChunk(name, flag, perm, first, offset, limited, buffer); err != nil {
			return err
		}
		offset += want - limited.N
		if remaining >= 0 {
			remaining -= want - limited.N
		}
		// Content ended before the chunk filled, or, when its length is known, the last chunk was written. A length that is a multiple of the chunk and not known costs one empty chunk.
		if limited.N > 0 || remaining == 0 {
			return nil
		}
	}
}

// remainingBytes is how many bytes content has left when that is known without reading it (a regular file, or a reader with Len), and -1 otherwise. Knowing it lets Write stop at the last chunk instead of opening the file once more to find the end.
func remainingBytes(content io.Reader) int64 {
	switch r := content.(type) {
	case *os.File:
		info, err := r.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return -1
		}
		position, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return -1
		}
		return max(info.Size()-position, 0)
	case interface{ Len() int }:
		return int64(r.Len())
	}
	return -1
}

// writeOnly hides the ReadFrom of a file, so that a copy through a buffer uses the buffer.
type writeOnly struct{ io.Writer }

// writeChunk copies limited's next chunk into name under the fork lock, starting at offset. The later chunks are not opened with O_APPEND: the kernel refuses copy_file_range into an append-mode descriptor, and the copy would fall back to a buffer.
func writeChunk(name string, flag int, perm fs.FileMode, first bool, offset int64, limited *io.LimitedReader, buffer []byte) (err error) {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if !first {
		flag = 0
	}
	file, err := os.OpenFile(name, flag|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if !first {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return errors.Join(err, file.Close())
		}
	}
	if limited.N > 0 {
		switch {
		case buffer != nil:
			_, err = io.CopyBuffer(writeOnly{file}, limited, buffer)
		default:
			if source, ok := limited.R.(*byteSource); ok {
				err = source.writeChunk(file, limited)
			} else {
				_, err = io.Copy(file, limited)
			}
		}
	}
	return errors.Join(err, file.Close())
}

// byteSource is the content of a Write that is already in memory. Write writes its chunks from the slice directly, with no buffer and no copy.
type byteSource struct{ data []byte }

// Bytes returns content for Write that reads data.
func Bytes(data []byte) io.Reader { return &byteSource{data} }

func (s *byteSource) Len() int { return len(s.data) }

func (s *byteSource) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.data)
	s.data = s.data[n:]
	return n, nil
}

// writeChunk writes the next limited.N bytes, or what is left, to file and counts them off limited.
func (s *byteSource) writeChunk(file *os.File, limited *io.LimitedReader) error {
	n := min(limited.N, int64(len(s.data)))
	_, err := file.Write(s.data[:n])
	s.data = s.data[n:]
	limited.N -= n
	return err
}
