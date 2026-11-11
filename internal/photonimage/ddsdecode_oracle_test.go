// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import "testing"

// TestDecodeDDSMatchesPhoton compares Decode with Photon on the corpus of ddsdecode_corpus_test.go (image 0.24.9's DDS container over its
// DXT decoder, BC1 to BC3; see comparePhotonCorpus).
func TestDecodeDDSMatchesPhoton(t *testing.T) {
	comparePhotonCorpus(t, "dds", ".dds", ddsCorpus())
}
