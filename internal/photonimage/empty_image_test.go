// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"testing"
)

// An image with a width or a height of zero, which Photon decodes (farbfeld, an interlaced GIF): the answers below are what Photon 0.3.3 and
// photon-node 0.3.4 gave under Node for a PhotonImage of 0x4, 4x0 and 0x0 (the probe is in the rows of branch fix/follow-photon-decode-e):
// `get_bytes()` throws, `get_bytes_jpeg(q)` returns 625 bytes (the SHA-256 prefixes below), `resize` of it gives transparent black.

func TestEncodePNGOfAnImageWithNoPixelsFails(t *testing.T) {
	for _, size := range [][2]int{{0, 4}, {4, 0}, {0, 0}} {
		if _, err := EncodePNG(image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))); !errors.Is(err, ErrPNGNoPixels) {
			t.Errorf("%dx%d: err = %v, want ErrPNGNoPixels", size[0], size[1], err)
		}
	}
}

func TestEncodeJPEGOfAnImageWithNoPixelsIsPhotons(t *testing.T) {
	qualities := []uint8{1, 40, 55, 70, 80, 100}
	for _, tc := range []struct {
		width, height int
		prefixes      [6]string
	}{
		{0, 4, [6]string{"973faf846d9edd23", "140609e04bebcfd5", "9610ed2bacd5e5fd", "68bd500dfc9e4630", "e42e5d9cd259a972", "278885f862034dd2"}},
		{4, 0, [6]string{"570d6c9559b1880e", "9e0ab3020cfe9e3b", "e1bae481e9452986", "61df71202222c8ac", "bba1e207dda10fc3", "bdf4a844d3cafc9e"}},
		{0, 0, [6]string{"249352b4cc68ba23", "5441d93b02c5a925", "bd8029347989df66", "58b41931b8367d71", "68d196c4c6767f93", "75da40cbd19b0c55"}},
	} {
		for i, q := range qualities {
			got, err := EncodeJPEG(image.NewNRGBA(image.Rect(0, 0, tc.width, tc.height)), q)
			if err != nil {
				t.Errorf("%dx%d q%d: %v", tc.width, tc.height, q, err)
				continue
			}
			sum := sha256.Sum256(got)
			if len(got) != 625 || hex.EncodeToString(sum[:8]) != tc.prefixes[i] {
				t.Errorf("%dx%d q%d: %d bytes, SHA-256 %x...; Photon: 625 bytes, %s...", tc.width, tc.height, q, len(got), sum[:8], tc.prefixes[i])
			}
		}
	}
	for _, size := range [][2]int{{0, 70000}, {70000, 0}} {
		if _, err := EncodeJPEG(image.NewNRGBA(image.Rect(0, 0, size[0], size[1])), 80); err == nil {
			t.Errorf("%dx%d: no error, Photon throws for a side over 65535", size[0], size[1])
		}
	}
}

func TestResizeLanczos3OfAnImageWithNoPixelsIsTransparentBlack(t *testing.T) {
	for _, src := range [][2]int{{0, 4}, {4, 0}, {0, 0}} {
		for _, dst := range [][2]int{{1, 4}, {4, 1}, {1, 1}, {2, 2}, {7, 3}} {
			got := ResizeLanczos3(image.NewNRGBA(image.Rect(0, 0, src[0], src[1])), dst[0], dst[1])
			if got.Rect.Dx() != dst[0] || got.Rect.Dy() != dst[1] {
				t.Fatalf("%v to %v: size %v", src, dst, got.Rect)
			}
			for _, v := range got.Pix {
				if v != 0 {
					t.Fatalf("%v to %v: a pixel is not transparent black", src, dst)
				}
			}
		}
		// A resize to a zero target is an empty image.
		if got := ResizeLanczos3(image.NewNRGBA(image.Rect(0, 0, src[0], src[1])), 0, 4); !got.Rect.Empty() {
			t.Errorf("%v to 0x4: %v", src, got.Rect)
		}
	}
}
