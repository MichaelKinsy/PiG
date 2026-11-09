package hosts_test

import (
	"bufio"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
)

// fakeSprite is a SpriteSource a test changes by hand.
type fakeSprite struct {
	mu        sync.Mutex
	asset     hosts.SpriteAsset
	has       bool
	listeners []func()
	closed    bool
}

func (f *fakeSprite) Current() (hosts.SpriteAsset, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asset, f.has
}

func (f *fakeSprite) OnChange(fn func()) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listeners = append(f.listeners, fn)
	return func() {}
}

func (f *fakeSprite) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

func (f *fakeSprite) set(key string, svg string) {
	f.mu.Lock()
	f.asset, f.has = hosts.SpriteAsset{Key: key, Name: "Name " + key, SVG: []byte(svg)}, key != ""
	listeners := append([]func(){}, f.listeners...)
	f.mu.Unlock()
	for _, fn := range listeners {
		fn()
	}
}

// spriteFixture is a server with the sprite source, and a function adding the local owner's cookie to request headers.
func spriteFixture(t *testing.T, source hosts.SpriteSource) (int, func(map[string]string) map[string]string) {
	t.Helper()
	enabled := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"devices"}, Web: &enabled, Sprite: source})
	return fixture.Server.Port, ownerOf(fixture)
}

const svgOne = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 14"><rect x="0" y="0" width="1" height="1" fill="#ff0000"/></svg>`
const svgTwo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 14"><rect x="1" y="1" width="1" height="1" fill="#00ff00"/></svg>`

// A selection is served as an SVG at the stable URL, named by the page, and revalidated by its key.
func TestSelectedSpriteIsServedAsSVG(t *testing.T) {
	source := &fakeSprite{}
	source.set("pink-1111", svgOne)
	port, owner := spriteFixture(t, source)

	asset := get(t, port, hosts.SpriteAssetPath+"?v=pink-1111", owner(nil))
	if asset.Status != 200 || asset.Body != svgOne || asset.Headers.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("asset: %d %q %q", asset.Status, asset.Headers.Get("Content-Type"), asset.Body)
	}
	if got := asset.Headers.Get("ETag"); got != `"pink-1111"` {
		t.Fatalf("ETag %q", got)
	}
	if again := get(t, port, hosts.SpriteAssetPath, owner(map[string]string{"If-None-Match": `"pink-1111"`})); again.Status != 304 || again.Body != "" {
		t.Fatalf("revalidation: %d %q", again.Status, again.Body)
	}
	page := get(t, port, "/", owner(nil)).Body
	for _, want := range []string{`data-brand-sprite="{&#34;key&#34;:&#34;pink-1111&#34;,&#34;name&#34;:&#34;Name pink-1111&#34;}"`, `<link rel="icon" href="` + hosts.SpriteAssetPath + `?v=pink-1111" type="image/svg+xml" />`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// Like the icons: signed out on an allowed host, yes; another site's page, no.
	if got := get(t, port, hosts.SpriteAssetPath, owner(map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "image"})).Status; got == 200 {
		t.Fatal("another site loaded the sprite")
	}
}

// With no selection there is no asset: the page keeps the brand's default pig and favicon, and still says it follows the hook.
func TestNoSelectionFallsBackToTheDefaultPig(t *testing.T) {
	port, owner := spriteFixture(t, &fakeSprite{})
	if got := get(t, port, hosts.SpriteAssetPath, owner(nil)).Status; got != 404 {
		t.Fatalf("asset without a selection: %d", got)
	}
	page := get(t, port, "/", owner(nil)).Body
	if !strings.Contains(page, `data-brand-sprite=""`) || !strings.Contains(page, `<link rel="icon" href="/icons/favicon.svg" type="image/svg+xml" />`) {
		t.Fatalf("page:\n%s", page)
	}
}

// Stock: without a source (the default) the server has neither route, the attribute nor the sprite favicon.
func TestWithoutASpriteSourceTheHookIsInert(t *testing.T) {
	port, owner := spriteFixture(t, nil)
	for _, path := range []string{hosts.SpriteAssetPath, hosts.SpriteEventsPath} {
		if got := get(t, port, path, owner(nil)).Status; got != 404 {
			t.Fatalf("%s: %d", path, got)
		}
	}
	page := get(t, port, "/", owner(nil)).Body
	if strings.Contains(page, "brand-sprite") || strings.Contains(page, "__p2_brand") {
		t.Fatalf("page mentions the hook:\n%s", page)
	}
}

// The event stream says the current key at once and each change after it, to a signed-in page only.
func TestSpriteEventsPushChanges(t *testing.T) {
	source := &fakeSprite{}
	source.set("pink-1111", svgOne)
	port, owner := spriteFixture(t, source)

	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+hosts.SpriteEventsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", owner(nil)["Cookie"])
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if text := scanner.Text(); strings.HasPrefix(text, "data: ") {
				lines <- strings.TrimPrefix(text, "data: ")
			}
		}
		close(lines)
	}()
	next := func() string {
		select {
		case line := <-lines:
			return line
		case <-time.After(10 * time.Second):
			t.Fatal("no event within 10 s")
			return ""
		}
	}
	if got := next(); got != `{"key":"pink-1111","name":"Name pink-1111"}` {
		t.Fatalf("first event %q", got)
	}
	source.set("kratos-2222", svgTwo)
	if got := next(); got != `{"key":"kratos-2222","name":"Name kratos-2222"}` {
		t.Fatalf("after a change: %q", got)
	}
	// The asset follows: the old URL's key is gone, the new one serves the new sprite.
	if body := get(t, port, hosts.SpriteAssetPath, owner(nil)).Body; body != svgTwo {
		t.Fatalf("asset after the change: %q", body)
	}
	source.set("", "")
	if got := next(); got != "" {
		t.Fatalf("after clearing: %q", got)
	}
	// Signed out (another site): no stream.
	if got := get(t, port, hosts.SpriteEventsPath, owner(map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "empty"})).Status; got == 200 {
		t.Fatal("another site read the stream")
	}
}
