// Regenerates photon-pngdecode-corpus.tsv: what Photon's `PhotonImage.new_from_byteslice(bytes)` makes of each PNG file of a directory, as the
// dimensions and the SHA-256 of `get_raw_pixels()` (RGBA8), or `error` where it throws. The files are the corpus of pngdecode_corpus_test.go,
// which TestDecodePNGMatchesPhoton writes to a directory when PHOTON_PNG_CORPUS_DIR is set:
//
//   PHOTON_PNG_CORPUS_DIR=/tmp/corpus go test -run TestDecodePNGMatchesPhoton ./internal/photonimage
//   node pngdecode_oracle.mjs /tmp/corpus <photon directory>... > photon-pngdecode-corpus.tsv
//
// It is decodeformats_oracle.mjs with the label `png` and the extension `.png`; see there for the photon directories (@silvia-odwyer/photon
// 0.3.3 and @silvia-odwyer/photon-node 0.3.4 embed png 0.17.14, fdeflate 0.3.6 and image 0.24.9) and the columns.
process.argv.splice(2, 0, "png", ".png");
await import("./decodeformats_oracle.mjs");
