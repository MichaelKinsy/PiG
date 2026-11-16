package extensionconformance

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestConformance_UIViewImageID pins, in every SDK and realization, that an image node (D614) carries the Kitty image id its
// author sets (upstream ImageOptions.imageId). The test hands each row its own token and its own id (a number no other row
// and no default uses); the fixture mounts an image node whose id, bytes and file name are built from the token.
//   - The attached client's snapshot holds the node and the view's `images` record, member for member: the id is the row's,
//     so it is neither absent (the host would allocate one) nor another row's. An SDK whose kit drops `imageId` sends a node
//     without it and its rows fail.
//   - `ref` is the SHA-256 of the bytes in `images`, which every kit computes for itself, and the node does not name the id
//     as `ref`: the two are different members of the node.
func TestConformance_UIViewImageID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for i, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		tok := fmt.Sprintf("row%d-%d", i, 7300+53*i)
		kittyID := 4_100_000 + 97*i
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			var mu sync.Mutex
			var drawn []uiview.Snapshot
			att, _, aerr := h.bridge.Views().Attach(gridClientAbilities("image"), func(d uiview.Delivery) bool {
				mu.Lock()
				defer mu.Unlock()
				if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "image-id" {
					drawn = append(drawn, d.Snapshot)
				}
				return true
			})
			if aerr != nil {
				t.Fatal(aerr)
			}
			t.Cleanup(func() {
				att.Detach()
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			runConformanceCommandArgs(t, h, "ui_view_image_id", fmt.Sprintf("%s %d", tok, kittyID))
			pollUntilConformance(t, "the probe never reported its mount", func() bool {
				return slices.ContainsFunc(viewNotifies(h), func(n string) bool { return strings.HasPrefix(n, "view-m-mounted:") })
			})
			if got, want := viewNotifies(h), []string{"view-m-mounted:live"}; !slices.Equal(got, want) {
				t.Fatalf("image id probe reported %q, want %q", got, want)
			}
			mu.Lock()
			sent := slices.Clone(drawn)
			mu.Unlock()
			if len(sent) != 1 {
				t.Fatalf("the client was sent the mount %d times, want once", len(sent))
			}
			bytes := []byte("pig-image-" + tok)
			sum := sha256.Sum256(bytes)
			ref := hex.EncodeToString(sum[:])
			wantView := map[string]any{
				"root":   map[string]any{"kind": "image", "id": tok + "-photo", "ref": ref, "mimeType": "image/png", "imageId": kittyID, "filename": tok + ".png"},
				"images": []any{map[string]any{"ref": ref, "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(bytes)}},
			}
			if got, want := viewCanonical(t, sent[0].View), viewCanonical(t, wantView); got != want || sent[0].Degraded {
				t.Errorf("the client was sent the view (degraded %v)\n  %s\nwant\n  %s", sent[0].Degraded, got, want)
			}
		})
	}
}
