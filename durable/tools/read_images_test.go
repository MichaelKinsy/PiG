package tools

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 42a3497d tools/read.ts: `read` returns an image as one image block instead of an unsupported_image error. Without an
// image processor, PNG, JPEG, GIF, and WebP within the model's image limits (by default 2000x2000 pixels and 4.5 MiB of
// base64) pass through as they are; anything else is an error result saying why.

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg=="

// pngOfSize is a PNG signature and IHDR declaring width x height: enough for the header checks, never decoded.
func pngOfSize(width, height uint32) []byte {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 'I', 'H', 'D', 'R'}
	png = binary.BigEndian.AppendUint32(png, width)
	png = binary.BigEndian.AppendUint32(png, height)
	return append(png, 8, 6, 0, 0, 0, 0, 0, 0, 0)
}

func TestReadReturnsAnImageWithinTheLimitsAsOneImageBlock(t *testing.T) {
	executionEnv := createEnv(t)
	png := must(base64.StdEncoding.DecodeString(onePixelPNG))
	writeText(t, executionEnv, "image.dat", png)
	result := mustRun(t, CreateReadTool(nil), map[string]any{"path": "image.dat"}, executionEnv)
	if result.IsError != nil {
		t.Fatalf("result = %+v", result.ToolExecutionResult)
	}
	want := []ai.ToolResultMessageContent{ai.ImageContent{Data: onePixelPNG, MimeType: "image/png"}}
	if len(result.Output) != 1 || result.Output[0] != want[0] {
		t.Fatalf("output = %+v, want %+v", result.Output, want)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0] != (durable.ToolDiagnostic{Severity: durable.SeverityInfo, Code: "image", Message: "Read image file [image/png]."}) {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestReadRefusesAnImageThatNeedsAProcessor(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "wide.png", pngOfSize(3000, 10))
	writeText(t, executionEnv, "tall.png", pngOfSize(10, 2001))
	for name, want := range map[string]string{
		"wide.png": "wide.png is an image (image/png) of 3000x10, larger than 2000x2000, and no image processor is configured to shrink it",
		"tall.png": "tall.png is an image (image/png) of 10x2001, larger than 2000x2000, and no image processor is configured to shrink it",
	} {
		result := mustRun(t, CreateReadTool(nil), map[string]any{"path": name}, executionEnv)
		if result.IsError == nil || !*result.IsError || len(result.Output) != 0 || len(result.Diagnostics) != 1 ||
			result.Diagnostics[0] != (durable.ToolDiagnostic{Severity: durable.SeverityError, Code: "unsupported_image", Message: want}) {
			t.Errorf("%s: result = %+v", name, result.ToolExecutionResult)
		}
	}
	// A BMP needs converting, which only a processor does.
	bmp := make([]byte, 70)
	copy(bmp, "BM")
	binary.LittleEndian.PutUint32(bmp[2:], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:], 54)
	binary.LittleEndian.PutUint32(bmp[14:], 40)
	binary.LittleEndian.PutUint16(bmp[26:], 1)
	binary.LittleEndian.PutUint16(bmp[28:], 24)
	writeText(t, executionEnv, "pic.bmp", bmp)
	result := mustRun(t, CreateReadTool(nil), map[string]any{"path": "pic.bmp"}, executionEnv)
	if result.IsError == nil || !*result.IsError || result.Diagnostics[0].Message != "pic.bmp is an image (image/bmp) that needs converting, and no image processor is configured" {
		t.Errorf("bmp result = %+v", result.ToolExecutionResult)
	}
}

func TestReadChecksTheBase64SizeAgainstTheModelsImageLimit(t *testing.T) {
	executionEnv := createEnv(t)
	png := append(pngOfSize(10, 10), make([]byte, 3000)...)
	writeText(t, executionEnv, "big.png", png)
	model := &ai.Model{ID: "m", Input: []string{"text", "image"},
		InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxBytes: 1000}}}}
	api := &fakeAPI{env: executionEnv, agent: durable.Agent{Model: &durable.ModelRef{Provider: "p", ModelId: "m"}}, models: modelsOf(model)}
	result, err := runWith(CreateReadTool(nil), map[string]any{"path": "big.png"}, api, background)
	mustDo(t, err)
	want := "big.png is an image (image/png) of 3.0KB, too large to send, and no image processor is configured to shrink it"
	if result.IsError == nil || !*result.IsError || result.Diagnostics[0].Message != want {
		t.Fatalf("result = %+v, want message %q", result.ToolExecutionResult, want)
	}
}

func TestReadNotesWhenTheModelTakesNoImages(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "image.dat", must(base64.StdEncoding.DecodeString(onePixelPNG)))
	model := &ai.Model{ID: "m", Input: []string{"text"}}
	api := &fakeAPI{env: executionEnv, agent: durable.Agent{Model: &durable.ModelRef{Provider: "p", ModelId: "m"}}, models: modelsOf(model)}
	result, err := runWith(CreateReadTool(nil), map[string]any{"path": "image.dat"}, api, background)
	mustDo(t, err)
	if result.IsError != nil || len(result.Output) != 1 ||
		!strings.HasSuffix(result.Diagnostics[0].Message, "The current model does not support images; it sees a placeholder instead.") {
		t.Fatalf("result = %+v", result.ToolExecutionResult)
	}
}

func TestReadDescribesImageSupport(t *testing.T) {
	if want := "Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to"; !strings.HasPrefix(CreateReadTool(nil).Description, want) {
		t.Fatalf("description = %q", CreateReadTool(nil).Description)
	}
}

// modelsOf is a durable.Models that knows one model.
type knownModels struct {
	durable.Models
	model *ai.Model
}

func (m knownModels) GetModel(provider, id string) *ai.Model {
	if provider == "p" && id == m.model.ID {
		return m.model
	}
	return nil
}

func modelsOf(model *ai.Model) durable.Models { return knownModels{model: model} }

// stubProcessor is an ImageProcessor that answers one prepared image (or none) and records what read gave it.
type stubProcessor struct {
	prepared *PreparedImage
	gotMime  string
	gotSize  int
	gotLimit ImageLimits
}

func (s *stubProcessor) Prepare(_ context.Context, bytes []byte, mimeType string, limits ImageLimits) (*PreparedImage, error) {
	s.gotMime, s.gotSize, s.gotLimit = mimeType, len(bytes), limits
	return s.prepared, nil
}

// pi: packages/durable/test/tools-images.test.ts "read of images, with the Photon image processor" and read.ts readImage: the
// processor's answer is the image block, and a converted or resized image is a note on the info diagnostic.
func TestReadGivesTheProcessorsImageAndNotesWhatItDid(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "wide.png", pngOfSize(4000, 1000))
	processor := &stubProcessor{prepared: &PreparedImage{
		Data: "AAAA", MimeType: "image/jpeg", ConvertedFrom: "image/png",
		Resized: &ImageResize{From: ImageSize{4000, 1000}, To: ImageSize{2000, 500}},
	}}
	result := mustRun(t, CreateReadTool(&ReadToolOptions{Images: processor}), map[string]any{"path": "wide.png"}, executionEnv)
	if processor.gotMime != "image/png" || processor.gotLimit != DEFAULT_IMAGE_LIMITS || processor.gotSize != len(pngOfSize(4000, 1000)) {
		t.Fatalf("processor saw %q %d %+v", processor.gotMime, processor.gotSize, processor.gotLimit)
	}
	if len(result.Output) != 1 || result.Output[0] != (ai.ImageContent{Data: "AAAA", MimeType: "image/jpeg"}) {
		t.Fatalf("output = %+v", result.Output)
	}
	want := "Read image file [image/jpeg]. Converted from image/png to image/jpeg. Resized from 4000x1000 to 2000x500. Multiply coordinates by 2.00 to map them to the original."
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Message != want || result.Diagnostics[0].Code != "image" {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestReadReportsAnImageTheProcessorCannotPrepare(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "broken.png", pngOfSize(4, 4))
	result := mustRun(t, CreateReadTool(&ReadToolOptions{Images: &stubProcessor{}}), map[string]any{"path": "broken.png"}, executionEnv)
	want := "broken.png is an image (image/png) that cannot be prepared for the model"
	if result.IsError == nil || !*result.IsError || len(result.Output) != 0 || result.Diagnostics[0].Message != want {
		t.Fatalf("result = %+v", result.ToolExecutionResult)
	}
}

func TestReadListsBmpInItsDescriptionOnlyWithAProcessor(t *testing.T) {
	if !strings.Contains(CreateReadTool(nil).Description, "(jpg, png, gif, webp)") {
		t.Fatalf("description = %q", CreateReadTool(nil).Description)
	}
	if d := CreateReadTool(&ReadToolOptions{Images: &stubProcessor{}}).Description; !strings.Contains(d, "(jpg, png, gif, webp, bmp)") {
		t.Fatalf("description with a processor = %q", d)
	}
}

func TestCreateCodingToolsGivesReadItsImageProcessor(t *testing.T) {
	executionEnv := createEnv(t)
	writeText(t, executionEnv, "pic.png", pngOfSize(4, 4))
	processor := &stubProcessor{prepared: &PreparedImage{Data: "QQ==", MimeType: "image/png"}}
	extension := CreateCodingTools(&ReadToolOptions{Images: processor})
	if extension.Name != "coding-tools" || len(extension.Tools) != 4 || extension.Tools[0].Name != "read" {
		t.Fatalf("extension = %+v", extension)
	}
	result := mustRun(t, extension.Tools[0], map[string]any{"path": "pic.png"}, executionEnv)
	if len(result.Output) != 1 || result.Output[0] != (ai.ImageContent{Data: "QQ==", MimeType: "image/png"}) {
		t.Fatalf("output = %+v", result.Output)
	}
}

func TestToBase64MatchesTheStandardEncodingForEveryRemainder(t *testing.T) {
	// Pi 1.2.0+b06c1f2c packages/durable/test/tools-images.test.ts "matches Buffer for every remainder length" (b0429971 adds the 8 KiB chunk edges)
	t.Run("matches Buffer for every remainder length", func(t *testing.T) {
		for _, length := range []int{0, 1, 2, 3, 4, 5, 8191, 8192, 8193, 10_000, 16_385} {
			bytes := make([]byte, length)
			for i := range bytes {
				bytes[i] = byte(i * 37)
			}
			if got, want := ToBase64(bytes), base64.StdEncoding.EncodeToString(bytes); got != want || int(Base64Length(int64(length))) != len(want) {
				t.Fatalf("length %d: %d vs %d", length, len(got), len(want))
			}
		}
	})
}
