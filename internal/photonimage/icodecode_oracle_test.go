// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import "testing"

// TestDecodeICOMatchesPhoton compares Decode with Photon on the corpus of icodecode_corpus_test.go (image 0.24.9's ICO decoder over its BMP
// decoder in ICO mode and the png crate; see comparePhotonCorpus).
func TestDecodeICOMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "ico", ".ico", icoCorpus())
}
