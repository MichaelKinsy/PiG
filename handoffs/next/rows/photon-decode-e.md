# Lane photon-decode-e rows (Photon's BMP and GIF decoders; PNG and the zero-size durable case are pending), branch fix/follow-photon-decode-e

Base `ba33bfe21a` (NextPin's head, which contains `fix/follow-photon-decode-d`). No Pi source file changes: the Rust that Pi runs is Photon (`@silvia-odwyer/photon` 0.3.3 for durable, `@silvia-odwyer/photon-node` 0.3.4 for the coding agent), whose wasm embeds image 0.24.9, gif 0.13.1, weezl 0.1.8 and png 0.17.14.

## Status

| item | status |
|---|---|
| BMP routing | done: `DecodeBMP` (`bmpdecode.go`, the ICO-mode port generalised), routed by `BM`; `golang.org/x/image/bmp` unregistered and banned by `decode_routing_test.go`; `decodeBitfieldBMP` deleted |
| GIF routing | done: `DecodeGIF` (`gifdecode.go`, `gifdecode_lzw.go`), routed by `GIF87a`/`GIF89a`; `image/gif` unregistered and banned |
| PNG routing | pending: `image/png` is still registered; the png reader of `icopng.go` decodes only the RGBA8 output an ICO entry needs. Missing: the other output colour types (L8, La8, Rgb8, L16, La16, Rgb16, Rgba16 under EXPAND, `transform.rs`), the cumulative `Limits.bytes` accounting of the crate (`reserve_current_chunk`, PLTE, tRNS, iCCP), the corpus (PngSuite incl. its corrupt `x*` files, image-rs `tests/images/png`, `pngSpec` streams) and its Photon table |
| zero-size durable case | pending: not investigated (farbfeld 0x4 / 4x0 under `image/x-icon`, `application/octet-stream`, `image/bmp`, `image/gif`; Pi's `images/index.ts` answer and the PNG encoder's `get_bytes()` bytes for width 0). Note from the GIF port: a GIF whose frame is interlaced with height 0 decodes to a 0xN image in Photon, so the case is reachable through GIF as well |

## Confirmation per format

| format | Go decoder against Photon on the corpus | PiG |
|---|---|---|
| BMP | 10280 files, 5371 decode in Photon, 4909 throw. Routed through Go the test fails 4630 files (2598 files Photon decodes are refused by `x/image/bmp`, plus image types that are not NRGBA). `TestGoBMPDecoderDiffersFromPhoton`: refuses 2598 that Photon decodes, accepts 2 that Photon refuses, other pixels for 3 (184 files that claim more than 2^28 pixels are not given to Go's decoder) | ported, 0 differences of 10280 |
| GIF | 4904 files, 2677 decode in Photon, 2227 throw. Routed through Go the test fails 2715 files. `TestGoGIFDecoderDiffersFromPhoton`: refuses 653 that Photon decodes, accepts 37 that Photon refuses, other pixels for 135 | ported, 0 differences of 4904 |
| PNG | not measured | pending |

## (a) upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/coding-agent/src/utils/image-convert.ts, clipboard-image.ts | ported (BMP, GIF) | internal/photonimage/{bmpdecode,gifdecode,gifdecode_lzw}.go through `photonimage.Decode`; internal/imageprocessing/decode_image.go `DecodeImage` | test:internal/photonimage/bmpdecode_oracle_test.go#TestDecodeBMPMatchesPhoton, test:internal/photonimage/gifdecode_oracle_test.go#TestDecodeGIFMatchesPhoton, test:internal/imageprocessing/decode_image_test.go#TestDecodeImageMatchesPhotonOnMalformedBitfieldBMP | a BMP or GIF is decoded or refused as Photon does |

## (b) PORT_MAP rows

| Rust source (crate, path:lines) | Go | note |
|---|---|---|
| image 0.24.9 src/codecs/bmp/decoder.rs:1-1330 (`BmpDecoder::new`, `new_with_ico_format`, `read_metadata`, `read_file_header`, palette, RLE4/8, 16/24/32-bit, bitfields) | internal/photonimage/bmpdecode.go `DecodeBMP`, `bmpDecoder` | one decoder for the file and the ICO form: the file header, RGB8 output (alpha only through an alpha mask, `RGB32`), the 3-channel overflow check and limit; the four-byte buffer gets alpha 255 at the end when there is no alpha channel |
| image 0.24.9 src/codecs/gif.rs:1-330 (`GifDecoder::new`, `read_image`) | internal/photonimage/gifdecode.go `DecodeGIF` | the first frame placed on the logical screen, the frame buffer limit (512 MiB minus the screen) and the wasm32 `usize` checks |
| gif 0.13.1 src/reader/{mod.rs,decoder.rs,converter.rs} | internal/photonimage/gifdecode.go `gifStream`, `gifInterlace`, `readIntoBuffer` | the byte state machine (the empty-extension quirk, the control extension rules, the 50 MB extension limit), RGBA conversion, interlace order, `odd-sized buffer` for an empty line, and the 8 KiB `BufReader` window that decides whether the code after the last pixel is read |
| weezl 0.1.8 src/decode.rs:557-1233 (`DecodeState::advance`, `LsbBuffer`, `Table`) with `Decoder::new(Lsb, min_code_size)` | internal/photonimage/gifdecode_lzw.go `gifLZW` | the sequential form of the burst decoder: a word that does not fit is kept whole, a full output reads the next code, a clear code ends the call |

## (c) upstream tests

None ported: Photon and the Rust crates have no Pi test here. New oracles against Photon (photon 0.3.3 and photon-node 0.3.4 agree on every file; `testdata/decodeformats_oracle.mjs`): `TestDecodeBMPMatchesPhoton` (10280 files; 52 real files from image-rs's BMP Suite copy in `testdata/bmp-real`), `TestDecodeGIFMatchesPhoton` (4904 files; 16 real files in `testdata/gif-real`). Mutation checks of the GIF port against the corpus: removing the `BufReader` window fails 1 file (`window-invalid-after-last-pad78`), removing the read of the code after a full output fails the interlaced files.

## (d) async contracts

None: the decoders are synchronous and pure.

## (e) interface IDs

None public. `photonimage.Decode`, `DecodeBMP` and `DecodeGIF` are in an internal package.

## Measurements

Red commits: BMP `891e25d8ad` (corpus and table with Decode giving the files to `x/image/bmp`: 4630 of 10280 fail; run with a scratch guard that refuses files claiming more than 2^28 pixels, because Go's decoder allocates the image from the header and the unguarded run exceeded 140 GB), GIF `eb4ea299c8` and `9633eab3bb` (2715 of 4904 fail). Green: `cbc986b4ca`, 0 differences in both.

Not measured (run out of the request budget): decode benchmarks against the Go decoders, the headless default linux_amd64 stripped size delta, the perfgate ceiling status (the ceiling is not edited), `make lint-changed`, `make test-expectation-lint`, the divergence gates, `go fix -diff`, and `go test` of `./internal/codingagent`.

Files touched: internal/photonimage/{bmpdecode,gifdecode,gifdecode_lzw,decode,icodecode}.go, their tests (`bmpdecode_corpus_test.go`, `bmpdecode_oracle_test.go`, `gifdecode_corpus_test.go`, `gifdecode_oracle_test.go`, `decode_routing_test.go`), testdata/{photon-bmpdecode-corpus.tsv,photon-gifdecode-corpus.tsv,bmp-real,gif-real}; internal/imageprocessing/{decode_image.go (was bmp_bitfields.go),decode_image_test.go}; REUSE.toml; THIRD_PARTY_NOTICES.md; changelog.d/photon-decode-e-*.md.
