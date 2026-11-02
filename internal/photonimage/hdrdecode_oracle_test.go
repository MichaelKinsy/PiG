// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import "testing"

// TestDecodeHDRMatchesPhoton compares Decode with Photon on the corpus of hdrdecode_corpus_test.go (image 0.24.9's Radiance HDR
// decoder over libm's powf and exp2f; see comparePhotonCorpus).
func TestDecodeHDRMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "hdr", ".hdr", hdrCorpus())
}
