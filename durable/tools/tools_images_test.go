package tools

// Ports the cases of packages/durable/test/tools-images.test.ts (Pi 1.2.0) that need no image processor: "read of images,
// without an image processor" (all but the two that pass Photon, which durable/images/node/tools_images_test.go ports), "read
// of an image that changes while it is read", "imageDimensions" and "toBase64" (read_images_test.go
// TestToBase64MatchesTheStandardEncodingForEveryRemainder). Pi builds its fixtures with Photon's encoders; these build the same
// pixels with Go's image/png and image/jpeg (JPEG quality 95, as Photon's get_bytes_jpeg(95)).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
)

// imagePixels is tools-images.test.ts pixels: RGBA of a width by height image, red, with a green top-left pixel.
func imagePixels(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{255, 0, 0, 255})
	}
	copy(img.Pix, []byte{0, 255, 0, 255})
	return img
}

// encodeImage is tools-images.test.ts encodeRgba: the image as PNG, or as JPEG at quality 95.
func encodeImage(t *testing.T, img image.Image, format string) []byte {
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

// encodeTestImage is tools-images.test.ts encode: imagePixels(width, height) encoded as format.
func encodeTestImage(t *testing.T, width, height int, format string) []byte {
	t.Helper()
	return encodeImage(t, imagePixels(width, height), format)
}

// noisyImagePNG is tools-images.test.ts noisyPng: noise, which neither PNG nor JPEG compresses well, so the byte limit
// decides. The generator is the JavaScript one, `seed = (seed * 1103515245 + 12345) >>> 0` in doubles, so each pixel byte
// is the one Pi's fixture has. In doubles the generator falls into a cycle of 419 values, which deflate would shrink, but
// Photon's PNG encoder leaves it at its raw size (4,841,533 bytes at 1100x1100 with @silvia-odwyer/photon 0.3.3), so the
// PNG is stored uncompressed here too.
func noisyImagePNG(t *testing.T, width, height int) []byte {
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

// bmpImage is tools-images.test.ts bmp: a 24-bit BMP, which providers do not take inline.
func bmpImage(width, height int) []byte {
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

// gifImage is tools-images.test.ts gif: a 1x1 GIF with its logical screen size set to width by height.
func gifImage(width, height uint16) []byte {
	data := must(base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"))
	binary.LittleEndian.PutUint16(data[6:], width)
	binary.LittleEndian.PutUint16(data[8:], height)
	return data
}

// tiffOrientation is tools-images.test.ts tiff: a TIFF header whose first directory holds only Orientation = orientation.
func tiffOrientation(orientation int, little bool) []byte {
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

// jpegWithOrientation is tools-images.test.ts withOrientation: the JPEG with an APP1 segment whose EXIF says orientation,
// after an APP0 segment when app0.
func jpegWithOrientation(data []byte, orientation int, little, app0 bool) []byte {
	exif := append([]byte("Exif\x00\x00"), tiffOrientation(orientation, little)...)
	app1 := append([]byte{0xff, 0xe1, byte((len(exif) + 2) >> 8), byte((len(exif) + 2) & 0xff)}, exif...)
	out := bytes.Clone(data[:2])
	if app0 {
		out = append(out, 0xff, 0xe0, 0, 16, 0x4a, 0x46, 0x49, 0x46, 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)
	}
	out = append(out, app1...)
	return append(out, data[2:]...)
}

// imageReadEnv is tools-images.test.ts readEnv: onRead sees each positional read of an opened file after it is done, and
// largest is the biggest read so far.
type imageReadEnv struct {
	*envnode.NodeExecutionEnv
	onRead  func(offset, length int64)
	mu      sync.Mutex
	largest int64
}

func newImageReadEnv(cwd string, onRead func(offset, length int64)) *imageReadEnv {
	watch := &imageReadEnv{NodeExecutionEnv: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd}), onRead: onRead}
	watch.Self = watch
	return watch
}

type imageReadReader struct {
	env.BinaryReader
	watch *imageReadEnv
}

func (reader imageReadReader) Read(ctx context.Context, offset, length int64) ([]byte, error) {
	reader.watch.mu.Lock()
	reader.watch.largest = max(reader.watch.largest, length)
	reader.watch.mu.Unlock()
	data, err := reader.BinaryReader.Read(ctx, offset, length)
	if reader.watch.onRead != nil {
		reader.watch.onRead(offset, length)
	}
	return data, err
}

func (watch *imageReadEnv) OpenBinaryReader(ctx context.Context, path string, options *env.OpenBinaryReaderOptions) (env.BinaryReader, error) {
	reader, err := watch.NodeExecutionEnv.OpenBinaryReader(ctx, path, options)
	if err != nil {
		return nil, err
	}
	return imageReadReader{BinaryReader: reader, watch: watch}, nil
}

// anyModel is tools-images.test.ts's models: getModel answers its one model whatever is asked.
type anyModel struct {
	durable.Models
	model *ai.Model
}

func (models anyModel) GetModel(string, string) *ai.Model { return models.model }

// imageReadOptions is the options of tools-images.test.ts read.
type imageReadOptions struct {
	input  []string
	resize *ai.ModelImageResizeOptions
	env    env.ExecutionEnv
}

// readImageFile is tools-images.test.ts read: run `read` on data written to name in dir, for a conversation whose model takes
// options.input (by default text and image).
func readImageFile(t *testing.T, dir, name string, data []byte, options imageReadOptions) (durable.ToolExecutionResult, error) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
	model := &ai.Model{Input: options.input}
	if model.Input == nil {
		model.Input = []string{"text", "image"}
	}
	if options.resize != nil {
		model.InputLimits = &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: options.resize}}
	}
	executionEnv := options.env
	if executionEnv == nil {
		executionEnv = envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: dir})
	}
	api := &fakeAPI{env: executionEnv, agent: durable.Agent{Model: &durable.ModelRef{Provider: "test", ModelId: "test"}}, models: anyModel{model: model}}
	return CreateReadTool(nil).Execute(background, map[string]any{"path": name}, api)
}

func mustReadImageFile(t *testing.T, dir, name string, data []byte, options imageReadOptions) durable.ToolExecutionResult {
	t.Helper()
	result, err := readImageFile(t, dir, name, data, options)
	mustDo(t, err)
	return result
}

// imageMessages is tools-images.test.ts messages: the diagnostics' messages.
func imageMessages(result durable.ToolExecutionResult) []string {
	messages := []string{}
	for _, diagnostic := range result.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	return messages
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

// refusedImage is toMatchObject({ output: [], isError: true }).
func refusedImage(result durable.ToolExecutionResult) bool {
	return result.Output != nil && len(result.Output) == 0 && result.IsError != nil && *result.IsError
}

func TestReadOfImagesWithoutAnImageProcessor(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:221 "returns a supported image within the limits as it is"
	t.Run("returns a supported image within the limits as it is", func(t *testing.T) {
		dir := t.TempDir()
		data := encodeTestImage(t, 8, 6, "png")
		result := mustReadImageFile(t, dir, "a.png", data, imageReadOptions{})
		if want := []ai.ToolResultMessageContent{ai.ImageContent{Data: ToBase64(data), MimeType: "image/png"}}; !reflect.DeepEqual(result.Output, want) {
			t.Fatalf("output = %+v, want %+v", result.Output, want)
		}
		if got := imageMessages(result); !reflect.DeepEqual(got, []string{"Read image file [image/png]."}) {
			t.Fatalf("messages = %q", got)
		}
		tiny := mustReadImageFile(t, dir, "a.gif", gifImage(1, 1), imageReadOptions{})
		if got, want := onlyImage(t, tiny), (ai.ImageContent{Data: ToBase64(gifImage(1, 1)), MimeType: "image/gif"}); got != want {
			t.Fatalf("gif = %+v, want %+v", got, want)
		}
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:230 "refuses an image too large to send without reading it"
	t.Run("refuses an image too large to send without reading it", func(t *testing.T) {
		dir := t.TempDir()
		watch := newImageReadEnv(dir, nil)
		result := mustReadImageFile(t, dir, "big.png", noisyImagePNG(t, 1100, 1100), imageReadOptions{env: watch})
		if !refusedImage(result) {
			t.Fatalf("result = %+v", result)
		}
		if message := imageMessages(result)[0]; !regexp.MustCompile(`^big\.png is an image \(image/png\) of .*, too large to send`).MatchString(message) {
			t.Errorf("message = %q", message)
		}
		// Only the header and the chunk walk, in blocks of at most 64 KiB.
		if watch.largest > 64*1024 {
			t.Errorf("largest read = %d bytes, want at most %d", watch.largest, 64*1024)
		}
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:239 "refuses an image wider or taller than the limits, by its header"
	t.Run("refuses an image wider or taller than the limits, by its header", func(t *testing.T) {
		dir := t.TempDir()
		wide := mustReadImageFile(t, dir, "wide.png", encodeTestImage(t, 2001, 2, "png"), imageReadOptions{})
		if !refusedImage(wide) {
			t.Fatalf("wide = %+v", wide)
		}
		if got, want := imageMessages(wide), []string{"wide.png is an image (image/png) of 2001x2, larger than 2000x2000, and no image processor is configured to shrink it"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("messages = %q, want %q", got, want)
		}
		tall := mustReadImageFile(t, dir, "tall.gif", gifImage(1, 3000), imageReadOptions{})
		if message := imageMessages(tall)[0]; !regexp.MustCompile(`^tall\.gif is an image \(image/gif\) of 1x3000, larger than 2000x2000`).MatchString(message) {
			t.Fatalf("message = %q", message)
		}
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:249 "refuses a format that needs converting"
	t.Run("refuses a format that needs converting", func(t *testing.T) {
		result := mustReadImageFile(t, t.TempDir(), "a.bmp", bmpImage(4, 4), imageReadOptions{})
		if !refusedImage(result) {
			t.Fatalf("result = %+v", result)
		}
		if got, want := imageMessages(result), []string{"a.bmp is an image (image/bmp) that needs converting, and no image processor is configured"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("messages = %q, want %q", got, want)
		}
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:257 "says when the model sees a placeholder instead of the image"
	t.Run("says when the model sees a placeholder instead of the image", func(t *testing.T) {
		result := mustReadImageFile(t, t.TempDir(), "a.png", encodeTestImage(t, 4, 4, "png"), imageReadOptions{input: []string{"text"}})
		if len(result.Output) != 1 {
			t.Fatalf("output = %+v, want one block", result.Output)
		}
		if got, want := imageMessages(result), []string{"Read image file [image/png]. The current model does not support images; it sees a placeholder instead."}; !reflect.DeepEqual(got, want) {
			t.Fatalf("messages = %q, want %q", got, want)
		}
	})
}

func TestReadOfAnImageThatChangesWhileItIsRead(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:280 "reads it again rather than return a cut-off image, even when it grew"
	t.Run("reads it again rather than return a cut-off image, even when it grew", func(t *testing.T) {
		dir := t.TempDir()
		data := encodeTestImage(t, 8, 6, "png")
		var wholeReads []int64
		watch := newImageReadEnv(dir, func(offset, length int64) {
			// A writer appends during the first read of the whole file; the second read sees it all.
			if offset != 0 || length < int64(len(data)) {
				return
			}
			wholeReads = append(wholeReads, length)
			if len(wholeReads) == 1 {
				appendToFile(t, filepath.Join(dir, "growing.png"), make([]byte, 16))
			}
		})
		result := mustReadImageFile(t, dir, "growing.png", data, imageReadOptions{env: watch})
		if want := []int64{int64(len(data)), int64(len(data)) + 16}; !reflect.DeepEqual(wholeReads, want) {
			t.Fatalf("whole reads = %v, want %v", wholeReads, want)
		}
		after := append(bytes.Clone(data), make([]byte, 16)...)
		if got := onlyImage(t, result).Data; got != ToBase64(after) {
			t.Errorf("data has %d base64 characters, want the grown file's %d", len(got), len(ToBase64(after)))
		}
	})

	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:295 "fails when it changes during the second read too"
	t.Run("fails when it changes during the second read too", func(t *testing.T) {
		dir := t.TempDir()
		data := encodeTestImage(t, 8, 6, "png")
		watch := newImageReadEnv(dir, func(offset, length int64) {
			if offset == 0 && length >= int64(len(data)) {
				appendToFile(t, filepath.Join(dir, "busy.png"), make([]byte, 1))
			}
		})
		_, err := readImageFile(t, dir, "busy.png", data, imageReadOptions{env: watch})
		expectFailure(t, err, regexp.QuoteMeta("busy.png changed while it was read"))
	})
}

// appendToFile is appendFileSync.
func appendToFile(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		t.Error(err)
	}
}

func TestImageDimensions(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/tools-images.test.ts:441 "reads the size each format declares in its header"
	t.Run("reads the size each format declares in its header", func(t *testing.T) {
		expect := func(data []byte, mimeType string, width, height int, ok bool) {
			t.Helper()
			gotWidth, gotHeight, gotOK := imageDimensions(data, mimeType)
			if gotWidth != width || gotHeight != height || gotOK != ok {
				t.Errorf("imageDimensions(%s) = %d, %d, %v; want %d, %d, %v", mimeType, gotWidth, gotHeight, gotOK, width, height, ok)
			}
		}
		expect(encodeTestImage(t, 7, 5, "png"), "image/png", 7, 5, true)
		expect(encodeTestImage(t, 7, 5, "jpeg"), "image/jpeg", 7, 5, true)
		expect(jpegWithOrientation(encodeTestImage(t, 7, 5, "jpeg"), 6, false, true), "image/jpeg", 7, 5, true)
		expect(gifImage(300, 200), "image/gif", 300, 200, true)
		webp := func(kind string, data ...byte) []byte {
			out := append([]byte("RIFF\x00\x00\x00\x00WEBP"+kind+"\x00\x00\x00\x00"), data...)
			return append(out, make([]byte, 16)...)
		}
		// VP8: frame tag, start code, then 14-bit width and height.
		expect(webp("VP8 ", 0, 0, 0, 0x9d, 0x01, 0x2a, 0x2c, 0x01, 0xc8, 0x00), "image/webp", 300, 200, true)
		// VP8L: signature, then width - 1 and height - 1 in 14 bits each.
		bits := uint32(299 | 199<<14)
		expect(webp("VP8L", 0x2f, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24)), "image/webp", 300, 200, true)
		// VP8X: flags, then width - 1 and height - 1 in 24 bits each.
		expect(webp("VP8X", 0, 0, 0, 0, 0x2b, 0x01, 0, 0xc7, 0, 0), "image/webp", 300, 200, true)
		expect([]byte{0xff, 0xd8, 0xff}, "image/jpeg", 0, 0, false)
	})
}
