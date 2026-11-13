//go:build linux || darwin

package viewendpoint

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// BenchmarkUpdateWithSocketClients measures what the clients of the socket cost the one call that reaches them: an
// update of a mount, made on the goroutine of the extension's call, never on the interactive loop. The terminal's
// in-process client is attached in every case; 0, 1 and 7 clients of the socket read beside it (7 fills the registry's
// 8 attachments).
func BenchmarkUpdateWithSocketClients(b *testing.B) {
	for _, clients := range []int{0, 1, 7} {
		b.Run(strconv.Itoa(clients), func(b *testing.B) {
			dir := testenv.ShortTempDir(b, "ve")
			h, _ := newHost()
			e := New(Options{Dir: filepath.Join(dir, "view-endpoint")})
			defer e.Close()
			e.Bind(h)
			e.mu.Lock()
			for e.working {
				e.idle.Wait()
			}
			e.mu.Unlock()
			var abilities uiview.ClientAbilities
			if err := json.Unmarshal([]byte(testAbilities), &abilities); err != nil {
				b.Fatal(err)
			}
			if _, _, err := h.reg.Attach(abilities, func(uiview.Delivery) bool { return true }); err != nil {
				b.Fatal(err)
			}
			for range clients {
				c, err := net.Dial("unix", e.Path())
				if err != nil {
					b.Fatal(err)
				}
				defer func() { _ = c.Close() }()
				if _, err := c.Write([]byte(`{"id":"attach","type":"ui_view_attach","abilities":` + testAbilities + "}\n")); err != nil {
					b.Fatal(err)
				}
				r := bufio.NewReader(c)
				if line, err := r.ReadString('\n'); err != nil || !strings.Contains(line, `"success":true`) {
					b.Fatalf("attach: %v %s", err, line)
				}
				go func() { _, _ = io.Copy(io.Discard, r) }()
			}
			// DecodeMount and Mount return *uiview.Error: their results get names of their own, so no nil pointer is
			// read as a non-nil error.
			mount, decodeErr := uiview.DecodeMount([]byte(`{"key":"board","slot":"panel","view":{"root":{"kind":"text","text":"0"}}}`))
			if decodeErr != nil {
				b.Fatal(decodeErr)
			}
			st, mountErr := h.reg.Mount("p", mount)
			if mountErr != nil {
				b.Fatal(mountErr)
			}
			ref := st.Mount
			pad := strings.Repeat("x", 2048)
			b.ReportAllocs()
			n := 0
			for b.Loop() {
				n++
				u, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":%q}}}`, ref.Key, ref.Epoch, ref.Rev, strconv.Itoa(n)+pad))
				if err != nil {
					b.Fatal(err)
				}
				st, err := h.reg.Update("p", u)
				if err != nil {
					b.Fatal(err)
				}
				ref = st.Mount
			}
		})
	}
}
