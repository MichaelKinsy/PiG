# Lane photon-jpeg rows (Photon's JPEG encoder), branch fix/follow-photon-jpeg

Base `38f883a7f5` (`fix/follow-pi-next`, Pi `e7a0df27`). Red `8baaa8589b`, production `95de1de195` (the port and the call sites) and `d991d827b9` (speed, `sinf`, D165, notices), fragments and rows on top. No Pi source file changes; the Rust that Pi runs is Photon (`@silvia-odwyer/photon` 0.3.3 for durable, `@silvia-odwyer/photon-node` 0.3.4 for the coding agent), whose wasm embeds image 0.24.9 and png 0.17.14 (the panic strings name `image-0.24.9\src\codecs\jpeg\encoder.rs`, and the `compiler_builtins::math::libm::sinf` symbol is present). Both wasm builds write the same JPEG and resize bytes for the whole corpus.

## (a) upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/durable/src/images/index.ts | ported | durable/images/images.go, internal/photonimage/{jpeg,png,lanczos,sinf}.go | test:durable/images/photon_differential_test.go#TestPreparedImagesAreWhatPhotonProduces, test:durable/images/photon_pi_oracle_test.go#TestPhotonImagesMatchPiOnACorpus, test:durable/images/photon_pi_boundaries_test.go#TestPhotonImagesMatchPiOnTheJPEGQualityLadder | `get_bytes_jpeg(quality)` is now the port of image 0.24.9's JpegEncoder; PNG-source cases compare every byte with Pi (tolerance only for JPEG-source cases, whose Go decoder differs). D165 narrowed to the JPEG and WebP decoders |
| packages/coding-agent/src/utils/image-resize-core.ts | ported | internal/imageprocessing/images.go (`encodeJPEG`) | test:internal/imageprocessing/resize_strategy_oracle_test.go#TestResizeImageDimensionStrategyMatchesPi (bytes compared), test:internal/imageprocessing/resize_format_oracle_test.go#TestResizeFormatChoiceMatchesPiOnRealisticImages (no exception map) | the smaller-of-PNG-and-JPEG choice of e7a0df27 now compares Photon's own sizes |
| packages/coding-agent/src/utils/image-process.ts | ported | internal/imageprocessing/images.go, bmp_bitfields.go | test:internal/imageprocessing/process_image_oracle_test.go#TestProcessImageMatchesPi (SHA-256 of the returned bytes compared; `d165ProcessImageFormatChoice` deleted) | 0 of 316 PNG, GIF, BMP and unrecognized inputs differ in any byte; the 44 that differ are JPEG (26 of 47) and WebP (18 of 37) inputs that Go decodes to other pixels |

## (b) PORT_MAP rows

| Rust source (crate, path:lines) | Go | note |
|---|---|---|
| image 0.24.9 src/codecs/jpeg/encoder.rs :34-150, :177-282, :352-424, :498-590, :620-666, :682-805, :810-836; transform.rs :54-196 (IJG jfdctint.c, libjpeg 9a); entropy.rs :4-63 | internal/photonimage/jpeg.go `EncodeJPEG` | byte for byte; lookups, a reciprocal quantizer and a 64-bit bit writer replace the f32 and byte-at-a-time forms, each proven equal (`jpeg_reference_test.go`) |
| libm 0.2.16 src/math/{sinf,k_sinf,k_cosf,rem_pio2f}.rs (FreeBSD msun) | internal/photonimage/sinf.go `sinf` | Photon's wasm calls compiler_builtins' libm `sinf`, which differs from the correctly rounded sine for 840 of 3,000,000 arguments in [0, 3pi]; `lanczos3` uses it |

## (c) upstream tests

None ported or designed out (Photon and Rust crates have no Pi test here). New oracles against Photon: `TestEncodeJPEGWritesPhotonsBytes` (`internal/photonimage`, `testdata/jpeg_oracle.mjs`, `photon-jpeg-corpus.tsv`), `TestEncodeJPEGWritesPhotonsBytesForTheScene`, `TestResizeLanczos3MatchesPhotonOnStripsAndNearOneRatios` (`testdata/resize_oracle.mjs`, `photon-resize-corpus.tsv`), and the strengthened Pi oracles listed in (a).

## (d) async contracts

None: the encoder is synchronous and pure.

## (e) interface IDs

None public. `photonimage.EncodeJPEG(*image.NRGBA, uint8) ([]byte, error)` is in an internal package; durable and the coding agent call it.

## Measurements

Red (the tests of this branch against the old Go `image/jpeg` in the pipeline, `go test -overlay` of the pre-port `images.go`): `TestProcessImageMatchesPi` 91 of 400 cases fail (png 17 of 106, gif 14 of 56, bmp 14 of 65, unrecognized 2 of 89 now differ besides the 44 decoder cases), `TestResizeImageDimensionStrategyMatchesPi` 43 of 200 fail (bytes), `TestEncodeJPEGWritesPhotonsBytes` does not build (no `EncodeJPEG`); at the red commit itself the same tests with Go's encoder fail the same way. Green: `TestEncodeJPEGWritesPhotonsBytes` 835 of 835 JPEGs (96 pictures: 1x1 to 4000x3000, qualities 1 to 100) SHA-256 identical to Photon's, `TestProcessImageMatchesPi` 356 of 400 identical (the 44 are decoders), `TestResizeImageDimensionStrategyMatchesPi` 200 of 200, format choice 72 of 72, durable oracles pass.

The sine: with `math.Sin` in the Lanczos kernel 19 of the 251 resizes of `photon-resize-corpus.tsv` differ from Photon's (one channel of one pixel, one step); with `sinf` 0 do. Case 281 of `TestProcessImageMatchesPi` (a 2023x46 PNG to 2000x45) is the instance that found it.

Benchmark (`BenchmarkEncodeJPEG`, quality 80, GOMAXPROCS 1, Xeon 8468 at load 15, minimum of 6 runs of 8):

| | literal port (95de1de195) | optimized (d991d827b9) | Go image/jpeg |
|---|---|---|---|
| 12 MP photo (4000x3000) ns/op | 313,100,000 | 172,800,000 | 414,800,000 |
| 12 MP photo B/op, allocs/op | 3,007,040, 9 | 3,007,040, 9 | 52,322,806, 12,032,018 |
| screenshot (1920x1080) ns/op | 79,800,000 | 45,100,000 | 86,300,000 |
| screenshot B/op, allocs/op | 3,048,000, 12 | 3,048,000, 12 | 10,453,488, 2,088,977 |

Profile of the literal port (12 MP, encoder only): `encodeRGB` block extraction 17%, `fdct` 17%, `rgbToYCbCr` 18%, `quantize` (`math.Round` and the float32 division) 23%, `writeBlock` and the bit writer 25%. The optimized form: RGB lookup tables, a reciprocal-multiply quantizer with the sign handled on magnitudes, a non-zero bitmap that skips zero runs, and a 64-bit accumulator that flushes 32 bits at a time; the biggest single gain was the quantizer (73 to 47 ms on the screenshot). Bytes unchanged throughout (the corpus test and the exhaustive component tests ran at every step).

Binary size, `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w"` of the base and of the head: `examples/headless` (the perfgate `binary.headless.default.linux_amd64.bytes`) 36,692,128 to 36,696,224 bytes (+4,096; ceiling 36,708,352, `TestHeadlessEmbedBinarySizeGate/default_linux_amd64` passes), `cmd/pig` 68,718,752 to 68,722,848 (+4,096).

Files touched: internal/photonimage/{jpeg,sinf}.go, lanczos.go (`sinf`), png.go (package comment); tests jpeg_oracle_test.go, jpeg_reference_test.go, jpeg_bench_test.go, resize_corpus_test.go, sinf_test.go; testdata corpus.mjs, jpeg_oracle.mjs, resize_oracle.mjs, photon-jpeg-corpus.tsv, photon-resize-corpus.tsv, scene-photon-300x200-q80.jpg; internal/imageprocessing/{images.go, bmp_bitfields.go} and its tests; durable/images/{images.go} and its tests; docs/parity/divergences/D165.md, DIVERGENCES.md, DIVERGENCE-IDS.txt, internal/pigdocs/content/divergences.md; THIRD_PARTY_NOTICES.md, LICENSES/{IJG,SunPro}.txt; changelog.d/photon-jpeg-*.md.
