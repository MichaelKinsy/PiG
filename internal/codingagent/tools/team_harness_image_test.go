package tools

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

// Upstream harness/tools/image.ts detects headers, not decodable images. In particular,
// three JPEG bytes and sixteen PNG bytes suffice, and acTL only matters before IDAT.
func TestHarnessImageMagicBoundaries(t *testing.T) {
	t.Parallel()
	png := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 13, 'I', 'H', 'D', 'R'}
	ihdr := append(append([]byte{}, png...), make([]byte, 17)...)
	chunk := func(base []byte, name string) []byte {
		return append(append(append([]byte{}, base...), 0, 0, 0, 0), append([]byte(name), 0, 0, 0, 0)...)
	}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, ""},
		{"jpeg-prefix-short", []byte{0xff, 0xd8}, ""},
		{"jpeg-prefix-only", []byte{0xff, 0xd8, 0xff}, "image/jpeg"},
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0}, "image/jpeg"},
		{"jpeg-ls", []byte{0xff, 0xd8, 0xff, 0xf7}, ""},
		{"png-signature-only", png[:8], ""},
		{"png-short-ihdr", png[:15], ""},
		{"png-minimal-ihdr", png, "image/png"},
		{"png-wrong-length", append(append([]byte{}, png[:11]...), 12, 'I', 'H', 'D', 'R'), ""},
		{"png-wrong-first-chunk", append(append([]byte{}, png[:12]...), 'I', 'D', 'A', 'T'), ""},
		{"png-actl-before-idat", chunk(chunk(ihdr, "acTL"), "IDAT"), ""},
		{"png-actl-after-idat", chunk(chunk(ihdr, "IDAT"), "acTL"), "image/png"},
		{"png-skips-other-chunks", chunk(chunk(ihdr, "tEXt"), "acTL"), ""},
		{"png-truncated-chunk", append(append([]byte{}, ihdr...), 0, 0, 0, 8, 't', 'E', 'X', 't'), "image/png"},
		{"png-overflow-length", append(append([]byte{}, ihdr...), 255, 255, 255, 255, 't', 'E', 'X', 't'), "image/png"},
		{"gif87", []byte("GIF87a"), "image/gif"},
		{"gif89", []byte("GIF89a"), "image/gif"},
		{"gif-short", []byte("GIF89"), ""},
		{"gif-text", []byte("GIF is text"), ""},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
		{"webp-short", []byte("RIFF\x00\x00\x00\x00WEB"), ""},
		{"riff-not-webp", []byte("RIFF\x00\x00\x00\x00WAVE"), ""},
		{"webp-not-riff", []byte("RIFX\x00\x00\x00\x00WEBP"), ""},
		{"bmp-short", bmpHeader(58, 54, 40, 1, 24)[:25], ""},
		{"bmp-info-short", bmpHeader(58, 54, 40, 1, 24)[:29], ""},
		{"bmp-core-minimal", bmpHeader(0, 26, 12, 1, 8)[:26], "image/bmp"},
		{"bmp-declared-small", bmpHeader(25, 54, 40, 1, 24), ""},
		{"bmp-offset-overlaps-header", bmpHeader(58, 53, 40, 1, 24), ""},
		{"bmp-offset-equals-file-size", bmpHeader(54, 54, 40, 1, 24), ""},
		{"bmp-offset-beyond-file-size", bmpHeader(54, 55, 40, 1, 24), ""},
		{"bmp-unsupported-dib", bmpHeader(58, 54, 39, 1, 24), ""},
		{"bmp-dib-too-large", bmpHeader(200, 139, 125, 1, 24), ""},
		{"bmp-header-arithmetic", bmpHeader(0, 26, 0xffffffff, 1, 24), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageprocessing.DetectSupportedImageMimeType(tc.data); got != tc.want {
				t.Errorf("DetectSupportedImageMimeType = %q, want %q", got, tc.want)
			}
			if got := SupportedImageMime(tc.data); got != tc.want {
				t.Errorf("read-tool SupportedImageMime = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHarnessImageBMPPlanesAndBitDepth(t *testing.T) {
	t.Parallel()
	// These are the exact isBmp allowlists, including both DIB header boundaries.
	for _, dib := range []uint32{12, 40, 124} {
		for _, bits := range []uint16{0, 1, 4, 7, 8, 16, 24, 32, 64} {
			for _, planes := range []uint16{0, 1, 2} {
				t.Run(fmt.Sprintf("dib%d/bpp%d/planes%d", dib, bits, planes), func(t *testing.T) {
					want := ""
					switch bits {
					case 1, 4, 8, 16, 24, 32:
						if planes == 1 {
							want = "image/bmp"
						}
					}
					if got := SupportedImageMime(bmpHeader(0, 14+dib, dib, planes, bits)); got != want {
						t.Fatalf("SupportedImageMime = %q, want %q", got, want)
					}
				})
			}
		}
	}
}
