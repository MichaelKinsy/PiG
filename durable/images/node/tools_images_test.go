package node

// Ports the cases of packages/durable/test/tools-images.test.ts (Pi 1.2.0) that use Photon: the two of "read of images,
// without an image processor" that pass it, "read of images, with the Photon image processor", "EXIF orientation" and
// "createNodePhotonImages". The rest are in durable/tools/tools_images_test.go. Pi's processor is createNodePhotonImages(),
// this package's CreateNodePhotonImages(nil). Each case runs in its own synctest bubble so the worker, which stays 30
// seconds after its last image, exits with the case.
//
// Pi builds its fixtures with Photon's encoders and decodes the results with Photon; these build the same pixels with Go's
// image/png and image/jpeg (JPEG quality 95, as get_bytes_jpeg(95)) and decode with Go's decoders.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/images"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

type rgbColor [3]uint8

// blockImage is tools-images.test.ts blocks: block-pixel squares coloured row by row from colors.
func blockImage(colors [][]rgbColor, block int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, len(colors[0])*block, len(colors)*block))
	for y := range img.Rect.Dy() {
		for x := range img.Rect.Dx() {
			c := colors[y/block][x/block]
			copy(img.Pix[img.PixOffset(x, y):], []byte{c[0], c[1], c[2], 255})
		}
	}
	return img
}

// pixelImage is tools-images.test.ts pixels: a width by height image, red, with a green top-left pixel.
func pixelImage(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{255, 0, 0, 255})
	}
	copy(img.Pix, []byte{0, 255, 0, 255})
	return img
}

// encodeRGBA is tools-images.test.ts encodeRgba: the image as PNG, or as JPEG at quality 95.
func encodeRGBA(t *testing.T, img image.Image, format string) []byte {
	t.Helper()
	var out bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 95})
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// encode is tools-images.test.ts encode: pixelImage(width, height) encoded as format.
func encode(t *testing.T, width, height int, format string) []byte {
	t.Helper()
	return encodeRGBA(t, pixelImage(width, height), format)
}

// noisyPhotonPNG is tools-images.test.ts noisyPng: noise, so the byte limit decides. The generator is the JavaScript one,
// `seed = (seed * 1103515245 + 12345) >>> 0` in doubles, so each pixel byte is the one Pi's fixture has. In doubles it falls
// into a cycle of 419 values, which deflate would shrink, but Photon's PNG encoder leaves it at its raw size (4,841,533 bytes
// at 1100x1100 with @silvia-odwyer/photon 0.3.3), so the PNG is stored uncompressed here too.
func noisyPhotonPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	seed := 1.0
	for index := range img.Pix {
		// The conversion keeps the product rounded on its own, as JavaScript does, rather than fused with the sum.
		seed = math.Mod(float64(seed*1103515245)+12345, 4294967296)
		if index%4 == 3 {
			img.Pix[index] = 255
		} else {
			img.Pix[index] = byte(uint32(seed) >> 24)
		}
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// bmp is tools-images.test.ts bmp: a 24-bit BMP, which providers do not take inline.
func bmp(width, height int) []byte {
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
	for y := range height {
		for x := range width {
			copy(out[54+y*row+x*3:], []byte{0, 0, 255})
		}
	}
	return out
}

// gif is tools-images.test.ts gif() (1x1).
func gif() []byte {
	data, _ := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
	return data
}

var exifHeader = []byte("Exif\x00\x00")

// tiff is tools-images.test.ts tiff: a TIFF header whose first directory holds only Orientation = orientation.
func tiff(orientation int, little bool) []byte {
	order := binary.ByteOrder(binary.BigEndian)
	out := make([]byte, 26)
	copy(out, "MM")
	if little {
		order = binary.LittleEndian
		copy(out, "II")
	}
	order.PutUint16(out[2:], 42)
	order.PutUint32(out[4:], 8)
	order.PutUint16(out[8:], 1)
	order.PutUint16(out[10:], 0x0112)
	order.PutUint16(out[12:], 3)
	order.PutUint32(out[14:], 1)
	order.PutUint16(out[18:], uint16(orientation))
	return out
}

// withOrientation is tools-images.test.ts withOrientation: the JPEG with an APP1 segment whose EXIF says orientation, after
// an APP0 segment when app0.
func withOrientation(data []byte, orientation int, little, app0 bool) []byte {
	exif := append(bytes.Clone(exifHeader), tiff(orientation, little)...)
	app1 := append([]byte{0xff, 0xe1, byte((len(exif) + 2) >> 8), byte((len(exif) + 2) & 0xff)}, exif...)
	out := bytes.Clone(data[:2])
	if app0 {
		out = append(out, 0xff, 0xe0, 0, 16, 0x4a, 0x46, 0x49, 0x46, 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)
	}
	out = append(out, app1...)
	return append(out, data[2:]...)
}

// webpWithOrientation is tools-images.test.ts webpWithOrientation: a RIFF WebP with a dummy "VP8 " chunk and an EXIF chunk
// saying orientation, its TIFF data bare or prefixed.
func webpWithOrientation(orientation int, prefixed bool) []byte {
	chunk := func(kind string, data []byte) []byte {
		out := append([]byte(kind), byte(len(data)), byte(len(data)>>8), 0, 0)
		out = append(out, data...)
		if len(data)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	var exif []byte
	if prefixed {
		exif = append(exif, exifHeader...)
	}
	exif = append(exif, tiff(orientation, true)...)
	body := append([]byte("WEBP"), chunk("VP8 ", []byte{1, 2, 3})...)
	body = append(body, chunk("EXIF", exif)...)
	return append(append([]byte("RIFF"), byte(len(body)), byte(len(body)>>8), 0, 0), body...)
}

// decodedImage is tools-images.test.ts decodedImage: the image the base64 data holds.
func decodedImage(t *testing.T, data string) *image.NRGBA {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the prepared image does not decode: %v", err)
	}
	out := image.NewNRGBA(image.Rect(0, 0, decoded.Bounds().Dx(), decoded.Bounds().Dy()))
	for y := range out.Rect.Dy() {
		for x := range out.Rect.Dx() {
			out.Set(x, y, decoded.At(decoded.Bounds().Min.X+x, decoded.Bounds().Min.Y+y))
		}
	}
	return out
}

func imageSize(img *image.NRGBA) tools.ImageSize {
	return tools.ImageSize{Width: img.Rect.Dx(), Height: img.Rect.Dy()}
}

// readAPI is tools-images.test.ts read's api: the environment, nothing collected, and a conversation whose model is model.
type readAPI struct {
	durable.ToolExecutionApi
	env   env.ExecutionEnv
	model *ai.Model
}

func (api *readAPI) Env() env.ExecutionEnv                            { return api.env }
func (api *readAPI) Output(any, ...env.ShellOutputSkip)               {}
func (api *readAPI) Diagnostic(durable.ToolDiagnostic)                {}
func (api *readAPI) Details(context.Context, durable.JsonValue) error { return nil }
func (api *readAPI) Agent(context.Context) (durable.Agent, error) {
	return durable.Agent{Model: &durable.ModelRef{Provider: "test", ModelId: "test"}}, nil
}
func (api *readAPI) Models() durable.Models { return anyModel{model: api.model} }

// anyModel answers its one model whatever is asked, as read's models.getModel does.
type anyModel struct {
	durable.Models
	model *ai.Model
}

func (models anyModel) GetModel(string, string) *ai.Model { return models.model }

type readOptions struct {
	images tools.ImageProcessor
	resize *ai.ModelImageResizeOptions
	tool   *durable.ToolRegistration
}

// read is tools-images.test.ts read: run `read` on data written to name in dir, for a conversation whose model takes text
// and images.
func read(t *testing.T, dir, name string, data []byte, options readOptions) durable.ToolExecutionResult {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
	model := &ai.Model{Input: []string{"text", "image"}}
	if options.resize != nil {
		model.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: options.resize}}
	}
	api := &readAPI{env: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: dir}), model: model}
	tool := options.tool
	if tool == nil {
		tool = tools.CreateReadTool(&tools.ReadToolOptions{Images: options.images})
	}
	result, err := tool.Execute(context.Background(), map[string]any{"path": name}, api)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// messages is tools-images.test.ts messages: the diagnostics' messages.
func messages(result durable.ToolExecutionResult) []string {
	out := []string{}
	for _, diagnostic := range result.Diagnostics {
		out = append(out, diagnostic.Message)
	}
	return out
}

// onlyImage is tools-images.test.ts onlyImage: the one image block of the output.
func onlyImage(t *testing.T, result durable.ToolExecutionResult) ai.ImageContent {
	t.Helper()
	if len(result.Output) != 1 {
		t.Fatalf("output = %+v, want one block", result.Output)
	}
	block, ok := result.Output[0].(ai.ImageContent)
	if !ok {
		t.Fatalf("output = %+v, want an image block", result.Output)
	}
	return block
}

// newProcessor is the case's createNodePhotonImages(options) inside its bubble; the worker exits when the bubble ends.
func newProcessor(t *testing.T, options *NodePhotonImagesOptions) tools.ImageProcessor {
	t.Helper()
	drainWorkers(t)
	processor, err := CreateNodePhotonImages(options)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func prepare(t *testing.T, processor tools.ImageProcessor, data []byte, mimeType string, limits tools.ImageLimits) *tools.PreparedImage {
	t.Helper()
	prepared, err := processor.Prepare(context.Background(), data, mimeType, limits)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestReadOfImagesWithoutAnImageProcessor(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:265 "applies the model's image limits instead of the defaults"
	t.Run("applies the model's image limits instead of the defaults", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			processor := newProcessor(t, nil)
			dir := t.TempDir()
			data := encode(t, 8, 6, "png")
			refused := read(t, dir, "a.png", data, readOptions{resize: &ai.ModelImageResizeOptions{MaxWidth: 4}})
			if message := messages(refused)[0]; !regexp.MustCompile(`of 8x6, larger than 4x2000,`).MatchString(message) {
				t.Fatalf("message = %q", message)
			}
			resized := read(t, dir, "a.png", data, readOptions{resize: &ai.ModelImageResizeOptions{MaxWidth: 4}, images: processor})
			if got := imageSize(decodedImage(t, onlyImage(t, resized).Data)); got != (tools.ImageSize{Width: 4, Height: 3}) {
				t.Fatalf("decoded size = %+v, want 4x3", got)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:273 "lists bmp in the description only with a processor"
	t.Run("lists bmp in the description only with a processor", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			processor := newProcessor(t, nil)
			if got := tools.CreateReadTool(nil).Description; !strings.Contains(got, "(jpg, png, gif, webp)") {
				t.Errorf("description = %q", got)
			}
			if got := tools.CreateReadTool(&tools.ReadToolOptions{Images: processor}).Description; !strings.Contains(got, "(jpg, png, gif, webp, bmp)") {
				t.Errorf("description with a processor = %q", got)
			}
		})
	})
}

func TestReadOfImagesWithThePhotonImageProcessor(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:305 "passes an image that needs no change through untouched"
	t.Run("passes an image that needs no change through untouched", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			processor := newProcessor(t, nil)
			dir := t.TempDir()
			data := encode(t, 8, 6, "png")
			result := read(t, dir, "a.png", data, readOptions{images: processor})
			if want := []ai.ToolResultMessageContent{ai.ImageContent{Data: tools.ToBase64(data), MimeType: "image/png"}}; !reflect.DeepEqual(result.Output, want) {
				t.Fatalf("output = %+v, want %+v", result.Output, want)
			}
			if got := onlyImage(t, read(t, dir, "a.gif", gif(), readOptions{images: processor})).MimeType; got != "image/gif" {
				t.Fatalf("gif mimeType = %q", got)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:312 "scales an image down to the maximum size, and says how to map coordinates back"
	t.Run("scales an image down to the maximum size, and says how to map coordinates back", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			processor := newProcessor(t, nil)
			result := read(t, t.TempDir(), "wide.png", encode(t, 4000, 1000, "png"), readOptions{images: processor})
			block := onlyImage(t, result)
			if got := imageSize(decodedImage(t, block.Data)); got != (tools.ImageSize{Width: 2000, Height: 500}) {
				t.Fatalf("decoded size = %+v", got)
			}
			want := []string{"Read image file [" + block.MimeType + "]. Resized from 4000x1000 to 2000x500. Multiply coordinates by 2.00 to map them to the original."}
			if got := messages(result); !reflect.DeepEqual(got, want) {
				t.Fatalf("messages = %q, want %q", got, want)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:322 "re-encodes a PNG too large for the byte limit as JPEG at its size, and shrinks it when that is not enough"
	t.Run("re-encodes a PNG too large for the byte limit as JPEG at its size, and shrinks it when that is not enough", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			processor := newProcessor(t, nil)
			result := read(t, t.TempDir(), "big.png", noisyPhotonPNG(t, 1100, 1100), readOptions{images: processor})
			converted := onlyImage(t, result)
			if converted.MimeType != "image/jpeg" {
				t.Fatalf("mimeType = %q", converted.MimeType)
			}
			if got, want := messages(result), []string{"Read image file [image/jpeg]. Converted from image/png to image/jpeg."}; !reflect.DeepEqual(got, want) {
				t.Fatalf("messages = %q, want %q", got, want)
			}
			if len(converted.Data) > tools.DEFAULT_IMAGE_LIMITS.MaxBytes {
				t.Fatalf("base64 length %d exceeds %d", len(converted.Data), tools.DEFAULT_IMAGE_LIMITS.MaxBytes)
			}
			if got := imageSize(decodedImage(t, converted.Data)); got != (tools.ImageSize{Width: 1100, Height: 1100}) {
				t.Fatalf("decoded size = %+v", got)
			}
			tight := tools.DEFAULT_IMAGE_LIMITS
			tight.MaxBytes = 200_000
			shrunk := prepare(t, processor, noisyPhotonPNG(t, 2000, 2000), "image/png", tight)
			if shrunk == nil {
				t.Fatal("prepared = nil")
			}
			if len(shrunk.Data) > tight.MaxBytes {
				t.Errorf("base64 length %d exceeds %d", len(shrunk.Data), tight.MaxBytes)
			}
			if shrunk.Resized == nil || shrunk.Resized.From != (tools.ImageSize{Width: 2000, Height: 2000}) || shrunk.Resized.To.Width >= 2000 {
				t.Errorf("resized = %+v", shrunk.Resized)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:336 "keeps at least one pixel on each side of an extreme aspect ratio"
	t.Run("keeps at least one pixel on each side of an extreme aspect ratio", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			result := prepare(t, newProcessor(t, nil), encode(t, 6000, 2, "png"), "image/png", tools.DEFAULT_IMAGE_LIMITS)
			want := tools.ImageResize{From: tools.ImageSize{Width: 6000, Height: 2}, To: tools.ImageSize{Width: 2000, Height: 1}}
			if result == nil || result.Resized == nil || *result.Resized != want {
				t.Fatalf("prepared = %+v, want resized %+v", result, want)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:341 "gives up when not even one pixel fits the byte limit"
	t.Run("gives up when not even one pixel fits the byte limit", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			limits := tools.DEFAULT_IMAGE_LIMITS
			limits.MaxBytes = 8
			if got := prepare(t, newProcessor(t, nil), encode(t, 4, 4, "png"), "image/png", limits); got != nil {
				t.Fatalf("prepared = %+v, want none", got)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:346 "keeps a JPEG that must be re-encoded a JPEG"
	t.Run("keeps a JPEG that must be re-encoded a JPEG", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			result := prepare(t, newProcessor(t, nil), withOrientation(encode(t, 40, 30, "jpeg"), 3, false, false), "image/jpeg", tools.DEFAULT_IMAGE_LIMITS)
			if result == nil || result.MimeType != "image/jpeg" {
				t.Fatalf("prepared = %+v", result)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:355 "converts a format providers do not take inline to PNG"
	t.Run("converts a format providers do not take inline to PNG", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			result := read(t, t.TempDir(), "a.bmp", bmp(4, 4), readOptions{images: newProcessor(t, nil)})
			if got := onlyImage(t, result).MimeType; got != "image/png" {
				t.Fatalf("mimeType = %q", got)
			}
			if got, want := messages(result), []string{"Read image file [image/png]. Converted from image/bmp to image/png."}; !reflect.DeepEqual(got, want) {
				t.Fatalf("messages = %q, want %q", got, want)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:361 "reports bytes it cannot decode as an error"
	t.Run("reports bytes it cannot decode as an error", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			broken := bytes.Clone(encode(t, 4, 4, "png")[:40])
			result := read(t, t.TempDir(), "broken.png", broken, readOptions{images: newProcessor(t, nil)})
			if result.Output == nil || len(result.Output) != 0 || result.IsError == nil || !*result.IsError {
				t.Fatalf("result = %+v", result)
			}
			if got, want := messages(result), []string{"broken.png is an image (image/png) that cannot be prepared for the model"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("messages = %q, want %q", got, want)
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:368 "is what createCodingTools({ images }) gives read"
	t.Run("is what createCodingTools({ images }) gives read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			extension := tools.CreateCodingTools(&tools.ReadToolOptions{Images: newProcessor(t, nil)})
			index := slices.IndexFunc(extension.Tools, func(candidate *durable.ToolRegistration) bool { return candidate.Name == "read" })
			if index < 0 {
				t.Fatal("createCodingTools has no read tool")
			}
			result := read(t, t.TempDir(), "b.bmp", bmp(4, 4), readOptions{tool: extension.Tools[index]})
			if got := onlyImage(t, result).MimeType; got != "image/png" {
				t.Fatalf("mimeType = %q", got)
			}
		})
	})
}

func TestEXIFOrientation(t *testing.T) {
	R, G, B, W, K, Y := rgbColor{255, 0, 0}, rgbColor{0, 255, 0}, rgbColor{0, 0, 255}, rgbColor{255, 255, 255}, rgbColor{0, 0, 0}, rgbColor{255, 255, 0}
	stored := [][]rgbColor{{R, G, B}, {W, K, Y}}
	mirror := func(grid [][]rgbColor) [][]rgbColor {
		out := make([][]rgbColor, len(grid))
		for i, row := range grid {
			out[i] = slices.Clone(row)
			slices.Reverse(out[i])
		}
		return out
	}
	// A quarter turn clockwise: the left column, read bottom to top, becomes the top row.
	turn := func(grid [][]rgbColor) [][]rgbColor {
		out := make([][]rgbColor, len(grid[0]))
		for x := range grid[0] {
			for _, g := range slices.Backward(grid) {
				out[x] = append(out[x], g[x])
			}
		}
		return out
	}
	// What each orientation shows, from its EXIF definition: mirror, then turn clockwise.
	upright := map[int][][]rgbColor{
		1: stored,
		2: mirror(stored),
		3: turn(turn(stored)),
		4: turn(turn(mirror(stored))),
		5: turn(turn(turn(mirror(stored)))),
		6: turn(stored),
		7: turn(mirror(stored)),
		8: turn(turn(turn(stored))),
	}

	for orientation := 1; orientation <= 8; orientation++ {
		// Pi 1.2.0 packages/durable/test/tools-images.test.ts:403 "turns orientation ${orientation} upright"
		t.Run("turns orientation "+string(rune('0'+orientation))+" upright", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				jpegBytes := withOrientation(encodeRGBA(t, blockImage(stored, 16), "jpeg"), orientation, false, false)
				prepared := prepare(t, newProcessor(t, nil), jpegBytes, "image/jpeg", tools.DEFAULT_IMAGE_LIMITS)
				if prepared == nil {
					t.Fatal("prepared = nil")
				}
				img := decodedImage(t, prepared.Data)
				expected := upright[orientation]
				if got, want := imageSize(img), (tools.ImageSize{Width: len(expected[0]) * 16, Height: len(expected) * 16}); got != want {
					t.Fatalf("size = %+v, want %+v", got, want)
				}
				// The colour at the centre of each block, rounded to full channels against JPEG noise.
				seen := make([][]rgbColor, len(expected))
				for by, row := range expected {
					for bx := range row {
						at := img.PixOffset(bx*16+8, by*16+8)
						var c rgbColor
						for channel := range c {
							if img.Pix[at+channel] > 127 {
								c[channel] = 255
							}
						}
						seen[by] = append(seen[by], c)
					}
				}
				if !reflect.DeepEqual(seen, expected) {
					t.Fatalf("blocks = %v, want %v", seen, expected)
				}
			})
		})
	}

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:424 "reads big- and little-endian JPEG EXIF, after an APP0 segment, and 1 when absent or out of range"
	t.Run("reads big- and little-endian JPEG EXIF, after an APP0 segment, and 1 when absent or out of range", func(t *testing.T) {
		jpegBytes := encode(t, 2, 2, "jpeg")
		for _, tc := range []struct {
			data []byte
			want int
		}{
			{withOrientation(jpegBytes, 6, false, false), 6},
			{withOrientation(jpegBytes, 8, true, false), 8},
			{withOrientation(jpegBytes, 5, false, true), 5},
			{withOrientation(jpegBytes, 9, false, false), 1},
			{jpegBytes, 1},
			{encode(t, 2, 2, "png"), 1},
		} {
			if got := images.ExifOrientation(tc.data); got != tc.want {
				t.Errorf("exifOrientation = %d, want %d", got, tc.want)
			}
		}
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:434 "reads WebP EXIF chunks with and without the Exif prefix"
	t.Run("reads WebP EXIF chunks with and without the Exif prefix", func(t *testing.T) {
		if got := images.ExifOrientation(webpWithOrientation(6, false)); got != 6 {
			t.Errorf("without the prefix = %d, want 6", got)
		}
		if got := images.ExifOrientation(webpWithOrientation(3, true)); got != 3 {
			t.Errorf("with the prefix = %d, want 3", got)
		}
	})
}

// goroutineProbe is the processor it wraps, recording whether each image was prepared on the worker's goroutine.
type goroutineProbe struct {
	inner    tools.ImageProcessor
	onWorker atomic.Bool
}

func (probe *goroutineProbe) Prepare(ctx context.Context, data []byte, mimeType string, limits tools.ImageLimits) (*tools.PreparedImage, error) {
	frames := make([]byte, 8192)
	probe.onWorker.Store(strings.Contains(string(frames[:runtime.Stack(frames, false)]), "node.(*workerImages).run"))
	return probe.inner.Prepare(ctx, data, mimeType, limits)
}

func TestCreateNodePhotonImages(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:497 "prepares in a worker thread, leaving the event loop free"
	//
	// Pi measures how long Node's one event loop is blocked while the photo is prepared: over 200 ms on the calling thread,
	// under 100 ms with the worker. Go has no event loop for a preparation to block (every goroutine keeps running while
	// another prepares), so what the case claims is ported as where the photo is prepared: both processors prepare it, the
	// plain one on the caller's goroutine and the worker's on the worker's goroutine.
	t.Run("prepares in a worker thread, leaving the event loop free", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			photo := noisyPhotonPNG(t, 3000, 2000)
			off := false
			inline := &goroutineProbe{inner: newProcessor(t, &NodePhotonImagesOptions{Worker: &off})}
			if prepared := prepare(t, inline, photo, "image/png", tools.DEFAULT_IMAGE_LIMITS); prepared == nil {
				t.Fatal("inline: prepared = nil")
			}
			if inline.onWorker.Load() {
				t.Error("worker: false prepared on a worker goroutine")
			}
			threaded := newProcessor(t, nil).(*workerImages)
			probe := &goroutineProbe{inner: threaded.inner}
			threaded.inner = probe
			if prepared := prepare(t, threaded, photo, "image/png", tools.DEFAULT_IMAGE_LIMITS); prepared == nil {
				t.Fatal("threaded: prepared = nil")
			}
			if !probe.onWorker.Load() {
				t.Error("the worker processor prepared on the caller's goroutine")
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:524 "gives the same result as the calling thread, and keeps the caller's bytes"
	t.Run("gives the same result as the calling thread, and keeps the caller's bytes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			data := noisyPhotonPNG(t, 1100, 1100)
			copied := bytes.Clone(data)
			threaded := prepare(t, newProcessor(t, nil), data, "image/png", tools.DEFAULT_IMAGE_LIMITS)
			off := false
			inline := prepare(t, newProcessor(t, &NodePhotonImagesOptions{Worker: &off}), data, "image/png", tools.DEFAULT_IMAGE_LIMITS)
			if !reflect.DeepEqual(threaded, inline) {
				t.Errorf("threaded = %+v, inline = %+v", threaded, inline)
			}
			if !bytes.Equal(data, copied) {
				t.Error("preparing changed the caller's bytes")
			}
		})
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:537 "prepares concurrent images, and starts a new worker after the idle one exited"
	t.Run("prepares concurrent images, and starts a new worker after the idle one exited", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			idle := 10
			processor := newProcessor(t, &NodePhotonImagesOptions{IdleMs: &idle})
			base := workerGoroutines()
			inputs := []struct {
				data     []byte
				mimeType string
			}{{bmp(4, 4), "image/bmp"}, {encode(t, 4000, 1000, "png"), "image/png"}, {[]byte{1, 2, 3}, "image/png"}}
			results := make([]*tools.PreparedImage, len(inputs))
			errs := make([]error, len(inputs))
			var wg sync.WaitGroup
			for i, input := range inputs {
				wg.Go(func() {
					results[i], errs[i] = processor.Prepare(context.Background(), input.data, input.mimeType, tools.DEFAULT_IMAGE_LIMITS)
				})
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("input %d: %v", i, err)
				}
			}
			if results[0] == nil || results[0].MimeType != "image/png" {
				t.Errorf("bmp = %+v", results[0])
			}
			if results[1] == nil || results[1].Resized == nil || results[1].Resized.To != (tools.ImageSize{Width: 2000, Height: 500}) {
				t.Errorf("wide png = %+v", results[1])
			}
			if results[2] != nil {
				t.Errorf("undecodable bytes = %+v, want none", results[2])
			}
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
			if got := workerGoroutines() - base; got != 0 {
				t.Errorf("after 100 ms idle: %d workers, want the idle one gone", got)
			}
			if later := prepare(t, processor, bmp(2, 2), "image/bmp", tools.DEFAULT_IMAGE_LIMITS); later == nil || later.MimeType != "image/png" {
				t.Errorf("later bmp = %+v", later)
			}
		})
	})
}
