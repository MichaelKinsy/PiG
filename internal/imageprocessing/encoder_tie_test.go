package imageprocessing

import (
	"bytes"
	"encoding/base64"
	"image"
	"strings"
	"testing"
)

// Pi e7a0df27 (D217) makes the smaller of the PNG and the JPEG candidate the answer. Which one is smaller is decided by the encoders: Photon's
// (the image crate: no chroma subsampling in JPEG, its own deflate for PNG) and Go's differ in size by more than the gap between the two
// candidates on small and synthetic images, and the oracle's Pi runs Photon. The oracles therefore do not demand the same format from both sides.
// They demand that each side's format is the one e7a0df27's rule picks from that side's own candidates (preferredFormat): Pi's candidates are
// measured in the oracle script with Photon, PiG's with PiG's encoders. A Pi that returns the PNG whenever it fits (the pinned build, which the
// D217 hook replaces) violates the rule on the candidates it was measured with, so the oracle fails without the hook; every other field of the answer
// is still compared exactly. The rule itself is also pinned by TestFirstFittingCandidatePrefersTheSmallerOfPNGAndJPEG and
// TestResizeImagePicksJPEGOverLargerPNGForPhotos.

// candidateSizes are the base64 sizes of the PNG and of the JPEG at the configured quality that one encoder makes of the same resized image.
type candidateSizes struct {
	PNG  int `json:"png"`
	JPEG int `json:"jpeg"`
}

// piDefaultMaxBytes is DEFAULT_MAX_BYTES of image-resize-core.ts: 4.5MB of base64 payload.
const piDefaultMaxBytes = 4.5 * 1024 * 1024

// preferredFormat is the format of the answer for a resized image whose candidates have these sizes: the smaller of the PNG and the JPEG
// (the PNG when they are equal) when it is under maxBytes, else the other when that is, and a lower-quality JPEG (image-resize-core.ts, the
// `preferred` list) when neither is.
func (s candidateSizes) preferredFormat(maxBytes int) string {
	first, second := "image/png", "image/jpeg"
	firstSize, secondSize := s.PNG, s.JPEG
	if s.JPEG < s.PNG {
		first, second = second, first
		firstSize, secondSize = secondSize, firstSize
	}
	switch {
	case firstSize < maxBytes:
		return first
	case secondSize < maxBytes:
		return second
	}
	return "image/jpeg"
}

// piPhotonCandidatesJS defines candidateSizes(bytes, width, height, quality) for an oracle script: the sizes of the PNG and the JPEG that Pi's
// Photon makes of bytes resized to width x height as image-resize-core.js tryEncodings does, or null when Photon cannot decode them.
const piPhotonCandidatesJS = `
const { loadPhoton } = await load("pi-coding-agent/utils/photon.js");
const { applyExifOrientation } = await load("pi-coding-agent/utils/exif-orientation.js");
const photon = await loadPhoton();
const base64Size = (bytes) => Buffer.byteLength(Buffer.from(bytes).toString("base64"), "utf-8");
function candidateSizes(bytes, width, height, quality) {
	let raw, image, resized;
	try {
		raw = photon.PhotonImage.new_from_byteslice(bytes);
		image = applyExifOrientation(photon, raw, bytes);
		if (image !== raw) raw.free();
		resized = photon.resize(image, width, height, photon.SamplingFilter.Lanczos3);
		return { png: base64Size(resized.get_bytes()), jpeg: base64Size(resized.get_bytes_jpeg(quality)) };
	} catch {
		return null;
	} finally {
		if (resized) resized.free();
		if (image) image.free();
	}
}
`

// goCandidateSizes are PiG's PNG and JPEG candidates for the image in raw resized to width x height; ok is false when raw does not decode.
func goCandidateSizes(t *testing.T, raw []byte, width, height, quality int) (sizes candidateSizes, ok bool) {
	t.Helper()
	src, err := decodeAutoOriented(raw)
	if err != nil || width < 1 || height < 1 {
		return candidateSizes{}, false
	}
	resized := resizeImage(src, width, height)
	pngBytes, err := encodePNG(resized)
	if err != nil {
		t.Fatal(err)
	}
	jpegBytes, err := encodeJPEG(resized, quality)
	if err != nil {
		t.Fatal(err)
	}
	return candidateSizes{PNG: base64.StdEncoding.EncodedLen(len(pngBytes)), JPEG: base64.StdEncoding.EncodedLen(len(jpegBytes))}, true
}

// outputSize is the pixel size of the image PiG returned.
func outputSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return config.Width, config.Height
}

// encoderTie reports whether PiG and Pi return different formats of the resized image raw only because their encoders size the candidates
// differently: each format is the preferredFormat of that side's own candidates. goWidth x goHeight is the size of PiG's answer.
func encoderTie(t *testing.T, raw []byte, goWidth, goHeight, quality, maxBytes int, goMIME, piMIME string, piSizes *candidateSizes) bool {
	t.Helper()
	if goMIME == piMIME || piSizes == nil {
		return false
	}
	goSizes, ok := goCandidateSizes(t, raw, goWidth, goHeight, quality)
	return ok && goSizes.preferredFormat(maxBytes) == goMIME && piSizes.preferredFormat(maxBytes) == piMIME
}

// hintsWithoutFormat compares conversion hints of an encoder tie: the hint names the format the image ended up in.
func hintsWithoutFormat(hints string) string {
	return strings.NewReplacer("image/jpeg", "image/*", "image/png", "image/*").Replace(hints)
}

// jpegQualityOf is the JPEG quality a resize uses: the option, or the default when it is zero.
func jpegQualityOf(quality int) int {
	if quality == 0 {
		return JPEGQuality
	}
	return quality
}

// preferredFormat is what the oracles hold both sides to, so it follows image-resize-core.ts's `preferred` list exactly: the smaller candidate first
// (the PNG on a tie), then the other, then a lower-quality JPEG.
func TestPreferredFormatFollowsPiE7a0df27(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sizes    candidateSizes
		maxBytes int
		want     string
	}{
		{"JPEG smaller", candidateSizes{PNG: 900, JPEG: 400}, 1000, "image/jpeg"},
		{"PNG smaller", candidateSizes{PNG: 400, JPEG: 900}, 1000, "image/png"},
		{"equal sizes keep the PNG", candidateSizes{PNG: 500, JPEG: 500}, 1000, "image/png"},
		{"the smaller JPEG is the only one under maxBytes", candidateSizes{PNG: 1500, JPEG: 900}, 1000, "image/jpeg"},
		{"the larger JPEG is the only one under maxBytes", candidateSizes{PNG: 900, JPEG: 1500}, 1000, "image/png"},
		{"a size equal to maxBytes does not fit", candidateSizes{PNG: 1000, JPEG: 1000}, 1000, "image/jpeg"},
		{"neither fits: a lower-quality JPEG", candidateSizes{PNG: 2000, JPEG: 1500}, 1000, "image/jpeg"},
	} {
		if got := tc.sizes.preferredFormat(tc.maxBytes); got != tc.want {
			t.Errorf("%s: preferredFormat(%+v, %d) = %s, want %s", tc.name, tc.sizes, tc.maxBytes, got, tc.want)
		}
	}
	if encoderTie(t, nil, 1, 1, 80, 1000, "image/png", "image/png", &candidateSizes{PNG: 1, JPEG: 2}) {
		t.Error("equal formats are not a tie")
	}
	if encoderTie(t, nil, 1, 1, 80, 1000, "image/png", "image/jpeg", nil) {
		t.Error("a tie needs Pi's measured candidates")
	}
}
