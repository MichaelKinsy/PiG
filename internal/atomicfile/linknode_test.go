//go:build !windows

package atomicfile

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// writeFileSync follows a link and writes where it ends. Where that is a named pipe or a device the node gets the bytes and stays what it was, the link
// stays a link, and no file is made beside either; where it is a missing file the file is created, as the link-to-a-missing-target test says. The order in
// WriteFile (follow the link, then look at what the target is) must give all three the end Pi's in-place write gives them.
func TestWriteFileThroughALinkEndsWhereAnInPlaceWriteEnds(t *testing.T) {
	const data = `{"provider-a":{"type":"api_key","key":"secret"}}`
	for _, cell := range []struct {
		name  string
		setup func(t *testing.T, dir, link string) (target string, check func(t *testing.T))
	}{
		{"a link to a named pipe", func(t *testing.T, dir, link string) (string, func(*testing.T)) {
			fifo := filepath.Join(dir, "fifo")
			if err := syscall.Mkfifo(fifo, 0o666); err != nil {
				t.Skipf("no named pipes here: %v", err)
			}
			reader, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })
			return fifo, func(t *testing.T) {
				if got, err := io.ReadAll(reader); string(got) != data {
					t.Errorf("the pipe's reader got %q (%v), want the bytes", got, err)
				}
				if info, err := os.Lstat(fifo); err != nil || info.Mode().Type() != os.ModeNamedPipe {
					t.Errorf("the target is now %v (%v), want a named pipe", info, err)
				}
			}
		}},
		{"a link to a device", func(t *testing.T, dir, link string) (string, func(*testing.T)) {
			if os.Getuid() == 0 {
				t.Skip("root creates files in /dev")
			}
			info, err := os.Stat(os.DevNull)
			if err != nil || info.Mode()&os.ModeCharDevice == 0 {
				t.Skipf("no character device at %s: %v", os.DevNull, err)
			}
			return os.DevNull, func(t *testing.T) {
				if info, err := os.Stat(os.DevNull); err != nil || info.Mode()&os.ModeCharDevice == 0 {
					t.Errorf("%s is now %v (%v), want it to stay a character device", os.DevNull, info, err)
				}
			}
		}},
		{"a dangling link", func(t *testing.T, dir, link string) (string, func(*testing.T)) {
			target := filepath.Join(dir, "created")
			return target, func(t *testing.T) {
				if got, err := os.ReadFile(target); err != nil || string(got) != data {
					t.Errorf("the target = %q (%v), want it created with the bytes", got, err)
				}
			}
		}},
	} {
		t.Run(cell.name, func(t *testing.T) {
			dir := t.TempDir()
			link := filepath.Join(dir, "link")
			target, check := cell.setup(t, dir, link)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := WriteFile(link, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if !isSymlink(t, link) {
				t.Error("the link was replaced, want it to stay a link")
			}
			check(t)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				switch entry.Name() {
				case "link", filepath.Base(target):
				default:
					t.Errorf("the write left %s in the directory", entry.Name())
				}
			}
		})
	}
}
