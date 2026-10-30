// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import "testing"

// TestDecodePNMMatchesPhoton compares Decode with Photon on the corpus of pnmdecode_corpus_test.go (image 0.24.9's PNM decoder, P1 to
// P7; see comparePhotonCorpus).
func TestDecodePNMMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "pnm", ".pnm", pnmCorpus())
}
