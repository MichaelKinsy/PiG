package node_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/png"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/durable/images/cloudflare"
	"github.com/MichaelKinsy/PiG/durable/images/node"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// Pi 1.2.0 packages/durable/test/images-wasm-module.test.ts:8 "createPhotonImages takes a compiled WebAssembly module, as
// Cloudflare Workers pass it": a 1x1 GIF is returned as it is, once it decodes. The Go processors decode natively (D165), so
// there is no WebAssembly module to compile and pass; the GIF goes through the Cloudflare entry, which stands for the
// module-taking createPhotonImages, and the Node entry.
func TestTheNodeAndCloudflareProcessorsPrepareA1x1GIF(t *testing.T) {
	t.Run("createPhotonImages takes a compiled WebAssembly module, as Cloudflare Workers pass it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			node.DrainWorkers(t)
			const data = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
			forNode, err := node.CreateNodePhotonImages(nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, processor := range map[string]tools.ImageProcessor{"node": forNode, "cloudflare": cloudflare.CreateCloudflarePhotonImages()} {
				raw := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x01D\x00;")
				prepared, err := processor.Prepare(context.Background(), raw, "image/gif", tools.DEFAULT_IMAGE_LIMITS)
				if want := (tools.PreparedImage{Data: data, MimeType: "image/gif"}); err != nil || prepared == nil || *prepared != want {
					t.Errorf("%s: prepared = %+v, %v; want %+v", name, prepared, err, want)
				}
			}
		})
	})
}

// bmp24 is a 24-bit BMP, which providers do not take inline.
func bmp24(width, height int) []byte {
	row := (width*3 + 3) / 4 * 4
	out := make([]byte, 54+row*height)
	copy(out, "BM")
	binary.LittleEndian.PutUint32(out[2:], uint32(len(out)))
	binary.LittleEndian.PutUint32(out[10:], 54)
	binary.LittleEndian.PutUint32(out[14:], 40)
	binary.LittleEndian.PutUint32(out[18:], uint32(width))
	binary.LittleEndian.PutUint32(out[22:], uint32(height))
	binary.LittleEndian.PutUint16(out[26:], 1)
	binary.LittleEndian.PutUint16(out[28:], 24)
	binary.LittleEndian.PutUint32(out[34:], uint32(row*height))
	return out
}

func pngOf(t *testing.T, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// pi: packages/durable/test/tools-images.test.ts "prepares concurrent images ...": a BMP, a PNG wider than the limit and
// undecodable bytes prepared at once each get their own result, and the processor serves a later image after them.
func TestTheNodeProcessorPreparesConcurrentImagesEachOnItsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		node.DrainWorkers(t)
		processor, err := node.CreateNodePhotonImages(nil)
		if err != nil {
			t.Fatal(err)
		}
		type input struct {
			data []byte
			mime string
		}
		inputs := []input{{bmp24(4, 4), "image/bmp"}, {pngOf(t, 4000, 1000), "image/png"}, {[]byte{1, 2, 3}, "image/png"}}
		results := make([]*tools.PreparedImage, len(inputs))
		errs := make([]error, len(inputs))
		var wg sync.WaitGroup
		for i, in := range inputs {
			wg.Go(func() {
				results[i], errs[i] = processor.Prepare(context.Background(), in.data, in.mime, tools.DEFAULT_IMAGE_LIMITS)
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("input %d: %v", i, err)
			}
		}
		if results[0] == nil || results[0].MimeType != "image/png" {
			t.Errorf("bmp result = %+v", results[0])
		}
		if results[1] == nil || results[1].Resized == nil || results[1].Resized.To != (tools.ImageSize{Width: 2000, Height: 500}) {
			t.Errorf("wide png result = %+v", results[1])
		}
		if results[2] != nil {
			t.Errorf("undecodable bytes result = %+v, want nil", results[2])
		}
		later, err := processor.Prepare(context.Background(), bmp24(2, 2), "image/bmp", tools.DEFAULT_IMAGE_LIMITS)
		if err != nil || later == nil || later.MimeType != "image/png" {
			t.Errorf("later image = %+v, %v", later, err)
		}
	})
}

// pi: packages/durable/test/tools-images.test.ts "gives the same result as the calling thread, and keeps the caller's
// bytes": preparing leaves the caller's bytes as they were, so preparing them again gives the same image.
func TestTheNodeProcessorKeepsTheCallersBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		node.DrainWorkers(t)
		processor, err := node.CreateNodePhotonImages(nil)
		if err != nil {
			t.Fatal(err)
		}
		data := bmp24(1100, 1100)
		copied := bytes.Clone(data)
		first, err := processor.Prepare(context.Background(), data, "image/bmp", tools.DEFAULT_IMAGE_LIMITS)
		if err != nil || first == nil {
			t.Fatalf("first = %+v, %v", first, err)
		}
		if !bytes.Equal(data, copied) {
			t.Error("Prepare changed the caller's bytes")
		}
		second, err := processor.Prepare(context.Background(), data, "image/bmp", tools.DEFAULT_IMAGE_LIMITS)
		if err != nil || !reflect.DeepEqual(first, second) {
			t.Errorf("second = %+v, %v; want %+v", second, err, first)
		}
	})
}
