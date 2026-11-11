// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import "testing"

// TestDecodeFarbfeldMatchesPhoton compares Decode with Photon on the corpus of farbfelddecode_corpus_test.go (image 0.24.9's farbfeld
// decoder; see comparePhotonCorpus).
func TestDecodeFarbfeldMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "farbfeld", ".ff", farbfeldCorpus())
}
