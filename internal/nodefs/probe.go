package nodefs

import (
	"os"
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
	return Exists(unsafe.String(unsafe.SliceData(p.buf), len(p.buf)))
}
