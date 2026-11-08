package hosts

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// The PiG sprite hook (pig additive: Pi has no sprites). PiG Standard lets its user choose the pig of the PiG mark (`/sprite`);
// the branded web UI draws the chosen one. The hook is a SpriteSource a branded executable passes in ServerOptions.Sprite: nil
// (Stock, and every test that does not ask) leaves the server as Pi's, without the routes below or the page's attributes, and
// only a bundle built with the brand layer (its mark reads them) can use it.

// SpriteAssetPath is the chosen sprite as an SVG: its pixels as rects, `?v=<key>` busting the caches when the selection
// changes. A public file like the icons (a home screen or a tab fetches the favicon without the page's cookies); not to
// another site's page.
const SpriteAssetPath = "/__p2_brand/sprite.svg"

// LoginAssetPath is the empty state's banner as an SVG: the sprite beside its "PiG." wordmark in the sprite's palette, no
// background of its own, the wordmark's colors adapted to the page theme (`?theme=light|dark`, dark when absent). Served like
// SpriteAssetPath.
const LoginAssetPath = "/__p2_brand/login.svg"

// SpriteEventsPath is a server-sent event stream for a signed-in page: each message is the current sprite's info (SpriteInfo as
// one line of JSON, "" when there is none), the first one at once, then one whenever the selection changes.
const SpriteEventsPath = "/__p2_brand/sprite/events"

// SpriteAsset is a rendered sprite: a key that names its content, its name and tagline, the sprite's SVG and the empty state's
// banner SVG for each page theme.
type SpriteAsset struct {
	Key     string
	Name    string
	Tagline string
	SVG     []byte
	// LoginLight and LoginDark are the banner for the light and the dark page theme.
	LoginLight []byte
	LoginDark  []byte
}

// SpriteInfo is what the page learns of the current sprite (the page's `data-brand-sprite` attribute and every event): the key
// that names its assets (`?v=<key>`) and the texts the empty state shows.
type SpriteInfo struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
}

// SpriteSource is where the selected sprite comes from.
type SpriteSource interface {
	// Current is the selected sprite; false when there is none (the page keeps the brand's default pig).
	Current() (SpriteAsset, bool)
	// OnChange registers fn to be called after the selection changes (Current then answers the new one); cancel unregisters it.
	OnChange(fn func()) (cancel func())
	// Close stops watching.
	Close()
}

// spriteHub fans a source's changes out to the open event streams.
type spriteHub struct {
	source SpriteSource
	cancel func()
	mu     sync.Mutex
	next   int
	subs   map[int]chan struct{}
}

func newSpriteHub(source SpriteSource) *spriteHub {
	hub := &spriteHub{source: source, subs: map[int]chan struct{}{}}
	hub.cancel = source.OnChange(hub.changed)
	return hub
}

func (h *spriteHub) changed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		select {
		case sub <- struct{}{}:
		default: // a change is pending already: the stream reads the latest key when it wakes
		}
	}
}

func (h *spriteHub) subscribe() (<-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.next
	h.next++
	sub := make(chan struct{}, 1)
	h.subs[id] = sub
	return sub, func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

func (h *spriteHub) close() {
	h.cancel()
	h.source.Close()
}

// info is the current sprite's SpriteInfo as JSON, "" for none.
func (h *spriteHub) info() string {
	asset, ok := h.source.Current()
	if !ok {
		return ""
	}
	encoded, err := json.Marshal(SpriteInfo{Key: asset.Key, Name: asset.Name, Tagline: asset.Tagline})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// serveAsset answers GET/HEAD of SpriteAssetPath and LoginAssetPath: the SVG, revalidated with its key as ETag (the page's `?v=`
// makes a changed sprite a new URL).
func (h *spriteHub) serveAsset(response http.ResponseWriter, request *http.Request) {
	asset, ok := h.source.Current()
	if !ok {
		sendText(response, http.StatusNotFound, "Not found")
		return
	}
	svg := asset.SVG
	etag := `"` + asset.Key + `"`
	if request.URL.EscapedPath() == LoginAssetPath {
		if request.URL.Query().Get("theme") == "light" {
			svg, etag = asset.LoginLight, `"`+asset.Key+`-login-light"`
		} else {
			svg, etag = asset.LoginDark, `"`+asset.Key+`-login-dark"`
		}
	}
	header := response.Header()
	header.Set("Content-Type", "image/svg+xml")
	header.Set("Cache-Control", "no-cache")
	header.Set("ETag", etag)
	if strings.Contains(request.Header.Get("If-None-Match"), etag) {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(svg)
	}
}

// serveEvents holds a text/event-stream open until the client goes: the key now, then each change.
func (h *spriteHub) serveEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		sendText(response, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	changes, unsubscribe := h.subscribe()
	defer unsubscribe()
	header := response.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	send := func() bool {
		_, err := response.Write([]byte("data: " + h.info() + "\n\n"))
		flusher.Flush()
		return err == nil
	}
	if !send() {
		return
	}
	for {
		select {
		case <-request.Context().Done():
			return
		case <-changes:
			if !send() {
				return
			}
		}
	}
}
