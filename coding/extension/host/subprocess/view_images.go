package subprocess

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// Image bounds of the component kit (D107, docs/plan/extension-component-kit.md §7).
const (
	viewImageMaxBytes      = 8 << 20  // pig additive (D107): spec §7 bound
	viewImageStoreMaxBytes = 32 << 20 // pig additive (D107): spec §7 bound
	viewImageStoreMaxCount = 64       // pig additive (D107): spec §7 bound
)

// viewImage is image bytes a connection sent once and frames reference by
// hash. b64 is the base64 form tui.Image renders from.
type viewImage struct {
	ref      string
	mimeType string
	data     []byte
	b64      string
	refs     int    // live frames referencing it
	used     uint64 // last use, for least-recently-used eviction
}

// viewImageStore holds one connection's image bytes. Live frames pin their
// images; eviction drops the least recently used unpinned image when the
// store passes its bounds, and reports the dropped refs so the SDK sends
// them again (ui.view.evicted).
type viewImageStore struct {
	mu     sync.Mutex
	images map[string]*viewImage
	bytes  int
	clock  uint64
}

func newViewImageStore() *viewImageStore {
	return &viewImageStore{images: map[string]*viewImage{}}
}

var errViewImageTooLarge = errors.New("view image exceeds 8 MiB")

// admit stores the images a frame carries, verifying each ref against its
// bytes, and pins every ref the frame's nodes name, in one step so that an
// image cannot be evicted between its arrival and its first frame. When a
// ref is missing (evicted before the SDK learned of it) nothing is pinned
// and missing lists the refs the SDK must send again. evicted lists the
// refs dropped to stay within bounds.
func (s *viewImageStore) admit(images []ViewImageData, refs []string) (pinned map[string]*viewImage, missing, evicted []string, err error) {
	decoded := make([]*viewImage, 0, len(images))
	for _, img := range images {
		if base64.StdEncoding.DecodedLen(len(img.Data)) > viewImageMaxBytes+3 {
			return nil, nil, nil, errViewImageTooLarge
		}
		data, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("view image %s: %w", img.Ref, err)
		}
		if len(data) > viewImageMaxBytes {
			return nil, nil, nil, errViewImageTooLarge
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != img.Ref {
			return nil, nil, nil, fmt.Errorf("view image ref %q does not match its bytes", img.Ref)
		}
		if img.MimeType == "" {
			return nil, nil, nil, fmt.Errorf("view image %s has no mimeType", img.Ref)
		}
		decoded = append(decoded, &viewImage{ref: img.Ref, mimeType: img.MimeType, data: data, b64: img.Data})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, img := range decoded {
		if _, ok := s.images[img.ref]; ok {
			continue
		}
		s.images[img.ref] = img
		s.bytes += len(img.data)
	}
	for _, ref := range refs {
		if _, ok := s.images[ref]; !ok {
			missing = append(missing, ref)
		}
	}
	if len(missing) == 0 && len(refs) > 0 {
		pinned = make(map[string]*viewImage, len(refs))
		for _, ref := range refs {
			img := s.images[ref]
			s.clock++
			img.used = s.clock
			if _, dup := pinned[ref]; !dup {
				img.refs++
				pinned[ref] = img
			}
		}
	}
	return pinned, missing, s.evictLocked(), nil
}

// release unpins a frame's images and returns the refs evicted now that
// they may be dropped.
func (s *viewImageStore) release(images map[string]*viewImage) []string {
	if len(images) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, img := range images {
		img.refs--
	}
	return s.evictLocked()
}

func (s *viewImageStore) evictLocked() []string {
	var evicted []string
	for s.bytes > viewImageStoreMaxBytes || len(s.images) > viewImageStoreMaxCount {
		var oldest *viewImage
		for _, img := range s.images {
			if img.refs == 0 && (oldest == nil || img.used < oldest.used) {
				oldest = img
			}
		}
		if oldest == nil {
			break // every image is pinned by a live frame
		}
		delete(s.images, oldest.ref)
		s.bytes -= len(oldest.data)
		evicted = append(evicted, oldest.ref)
	}
	return evicted
}
