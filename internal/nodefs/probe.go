package nodefs

import (
	"os"
	"path/filepath"
	"unsafe"
)

// PathProbe asks fs.existsSync of paths below a directory without building a path string per probe: an ancestor walk probes
// the same few names at every level. The zero value is ready to use; one PathProbe serves one goroutine.
type PathProbe struct{ buf []byte }

// Set makes the probe's path path.join(dir, elems...) for a clean dir and elements that are plain names.
func (p *PathProbe) Set(dir string, elems ...string) *PathProbe {
	p.buf = append(p.buf[:0], dir...)
	for _, elem := range elems {
		if len(p.buf) > 0 && !os.IsPathSeparator(p.buf[len(p.buf)-1]) {
			p.buf = append(p.buf, os.PathSeparator)
		}
		p.buf = append(p.buf, elem...)
	}
	return p
}

// Is reports whether the probe's path is path.
func (p *PathProbe) Is(path string) bool { return string(p.buf) == path }

// Exists is fs.existsSync of the probe's path.
func (p *PathProbe) Exists() bool {
	// Exists keeps no reference to its argument, so it can read the buffer in place.
	return Exists(unsafe.String(unsafe.SliceData(p.buf), len(p.buf))) //nolint:gosec // G103: Exists keeps no reference to its argument, so the buffer is read in place without a copy; the bytes are never mutated while the string lives
}

// ParentDir is filepath.Dir of dir, and whether dir was already clean. clean reports that dir is a clean path, so
// the parent is dir sliced at its last separator, which costs no allocation; filepath.Dir cleans, and on Windows
// builds a new string for every path with a volume name. A caller passes the returned parent back with
// clean true. The first call of a walk passes clean false, as the caller's own path may be unclean.
func ParentDir(dir string, clean bool) string {
	if !clean {
		return filepath.Dir(dir)
	}
	v := len(filepath.VolumeName(dir))
	rest := dir[v:]
	if len(rest) == 0 || (len(rest) == 1 && os.IsPathSeparator(rest[0])) || !os.IsPathSeparator(rest[0]) {
		return filepath.Dir(dir)
	}
	i := len(rest) - 1
	for i > 0 && !os.IsPathSeparator(rest[i]) {
		i--
	}
	if i == 0 {
		return dir[:v+1]
	}
	return dir[:v+i]
}
