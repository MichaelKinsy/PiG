package images_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/tools"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// D165: runs Pi's images/index.ts createPhotonImages (Photon 0.3.3 under Node, types stripped) and PiG's CreatePhotonImages over
// one corpus and compares what the model receives: whether an image can be prepared, its format, the resized and convertedFrom
// records, the decoded size, byte-exact output when Pi passes the image through, and the pixels within the D165 tolerance
// (PNG: Lanczos3 differences only; JPEG: two encoders). The limits of every case leave the choice of PNG or JPEG and the
// number of 3/4 shrinks beyond what the two encoders' sizes can change.
type oracleCase struct {
	name      string
	data      []byte
	mime      string
	limits    tools.ImageLimits
	tolerance float64 // mean absolute channel error of the decoded images, 0 = bytes must be equal
	skipPix   bool    // compare size only (noise: JPEG encoders differ widely on it)
	quantized bool    // a JPEG result must carry the quantization table of the quality Pi chose
}

func oracleCorpus(t *testing.T) []oracleCase {
	t.Helper()
	def := tools.DEFAULT_IMAGE_LIMITS
	scene, err := os.ReadFile("testdata/scene.png")
	if err != nil {
		t.Fatal(err)
	}
	blockSource := rgbaOf(48, 32, func(x, y int) color.NRGBA {
		return [][]color.NRGBA{{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}, {{255, 255, 255, 255}, {0, 0, 0, 255}, {255, 255, 0, 255}}}[y/16][x/16]
	})
	cases := []oracleCase{
		{name: "png within the limits", data: encodePNG(t, pixels(8, 6)), mime: "image/png", limits: def},
		{name: "gif within the limits", data: gif(), mime: "image/gif", limits: def},
		{name: "jpeg within the limits", data: encodeJPEG(t, pixels(40, 30)), mime: "image/jpeg", limits: def},
		{name: "png wider than the limit", data: encodePNG(t, pixels(4000, 1000)), mime: "image/png", limits: def, tolerance: 1},
		{name: "png whose resized height rounds up", data: encodePNG(t, pixels(2999, 1000)), mime: "image/png", limits: def, tolerance: 1},
		{name: "png of an extreme aspect ratio", data: encodePNG(t, pixels(6000, 2)), mime: "image/png", limits: def, tolerance: 1},
		{name: "bmp becomes png", data: bmp(4, 4), mime: "image/bmp", limits: def, tolerance: 1},
		{name: "model limit narrower than the image", data: scene, mime: "image/png", limits: tools.ImageLimits{MaxWidth: 300, MaxHeight: 300, MaxBytes: def.MaxBytes}, tolerance: 1},
		{name: "model limit shorter than the image", data: scene, mime: "image/png", limits: tools.ImageLimits{MaxWidth: 2000, MaxHeight: 100, MaxBytes: def.MaxBytes}, tolerance: 1},
		{name: "png too large for the byte limit", data: noisyPNG(t, 1100, 1100), mime: "image/png", limits: def, skipPix: true},
		{name: "not even one pixel fits", data: encodePNG(t, pixels(4, 4)), mime: "image/png", limits: tools.ImageLimits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 8}},
		{name: "truncated png", data: encodePNG(t, pixels(4, 4))[:40], mime: "image/png", limits: def},
		{name: "not an image", data: []byte("definitely not a png"), mime: "image/png", limits: def},
		{name: "damaged gif", data: gif()[:20], mime: "image/gif", limits: def},
	}
	jpegSource := encodeJPEG(t, blockSource)
	for orientation := 1; orientation <= 8; orientation++ {
		cases = append(cases, oracleCase{
			name: "jpeg with EXIF orientation " + string(rune('0'+orientation)),
			data: withOrientation(jpegSource, orientation, orientation%2 == 0, orientation == 5), mime: "image/jpeg", limits: def, tolerance: 6,
		})
	}
	cases = append(cases, oracleCase{name: "jpeg that must be resized", data: withOrientation(encodeJPEG(t, pixels(3000, 1500)), 3, false, false), mime: "image/jpeg", limits: def, tolerance: 6})
	return cases
}

func TestPhotonImagesMatchPiOnACorpus(t *testing.T) { comparePhotonToPi(t, oracleCorpus(t)) }

// piResult is what Pi's prepare answered for one case: nil when it could not prepare the image.
type piResult struct {
	Data          string `json:"data"`
	MimeType      string `json:"mimeType"`
	ConvertedFrom string `json:"convertedFrom"`
	Resized       *struct {
		From struct{ Width, Height int } `json:"from"`
		To   struct{ Width, Height int } `json:"to"`
	} `json:"resized"`
}

// askPi runs Pi's images/index.ts createPhotonImages over the corpus under Node and returns its answers.
func askPi(t *testing.T, corpus []oracleCase) []*piResult {
	t.Helper()
	type wireCase struct {
		Bytes    string         `json:"bytes"`
		MimeType string         `json:"mimeType"`
		Limits   map[string]any `json:"limits"`
	}
	var payload []wireCase
	for _, c := range corpus {
		payload = append(payload, wireCase{Bytes: base64.StdEncoding.EncodeToString(c.data), MimeType: c.mime,
			Limits: map[string]any{"maxWidth": c.limits.MaxWidth, "maxHeight": c.limits.MaxHeight, "maxBytes": c.limits.MaxBytes}})
	}
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/images_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []*piResult
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(corpus) {
		t.Fatalf("oracle output: %v", err)
	}
	return pi
}

// comparePhotonToPi prepares every case on Pi (images/index.ts under Node) and on PiG and compares the two.
func comparePhotonToPi(t *testing.T, corpus []oracleCase) {
	t.Helper()
	pi := askPi(t, corpus)
	for i, c := range corpus {
		want := pi[i]
		got := prepare(t, c.data, c.mime, c.limits)
		if (want == nil) != (got == nil) {
			t.Errorf("%s: Pi prepared=%v, PiG prepared=%v", c.name, want != nil, got != nil)
			continue
		}
		if want == nil {
			continue
		}
		if got.MimeType != want.MimeType || got.ConvertedFrom != want.ConvertedFrom {
			t.Errorf("%s: PiG %s (from %q), Pi %s (from %q)", c.name, got.MimeType, got.ConvertedFrom, want.MimeType, want.ConvertedFrom)
		}
		switch {
		case (want.Resized == nil) != (got.Resized == nil):
			t.Errorf("%s: PiG resized=%v, Pi resized=%v", c.name, got.Resized, want.Resized)
		case want.Resized != nil && (got.Resized.From != (tools.ImageSize{Width: want.Resized.From.Width, Height: want.Resized.From.Height}) ||
			got.Resized.To != (tools.ImageSize{Width: want.Resized.To.Width, Height: want.Resized.To.Height})):
			t.Errorf("%s: PiG resized %+v, Pi resized %+v", c.name, *got.Resized, *want.Resized)
		}
		if c.quantized {
			ours, theirs := jpegQuantizationTable(t, got.Data), jpegQuantizationTable(t, want.Data)
			t.Logf("%s: first quantization table entries PiG %v, Pi %v (%d bytes, %d bytes)", c.name, ours[:2], theirs[:2], len(got.Data), len(want.Data))
			if !slices.Equal(ours, theirs) {
				t.Errorf("%s: PiG encoded at the quantization table %v, Pi at %v", c.name, ours, theirs)
			}
		}
		if c.tolerance == 0 && !c.skipPix {
			if got.Data != want.Data {
				t.Errorf("%s: Pi passed the image through; PiG's data differs", c.name)
			}
			continue
		}
		ours, theirs := decode(t, got.Data), decode(t, want.Data)
		if ours.Bounds() != theirs.Bounds() {
			t.Errorf("%s: decoded size PiG %v, Pi %v", c.name, ours.Bounds(), theirs.Bounds())
			continue
		}
		if c.skipPix {
			continue
		}
		mean := meanChannelError(ours, theirs)
		t.Logf("%s: mean absolute channel error against Pi %.3f (tolerance %.1f)", c.name, mean, c.tolerance)
		if mean > c.tolerance {
			t.Errorf("%s: mean absolute channel error %.3f against Pi exceeds %.1f", c.name, mean, c.tolerance)
		}
	}
}
