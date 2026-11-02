# Lane photon-decode-e rows (Photon's BMP, GIF and PNG decoders and the zero-size image), branch fix/follow-photon-decode-e

Base `ba33bfe21a` (NextPin's head, which contains `fix/follow-photon-decode-d`). No Pi source file changes: the Rust that Pi runs is Photon (`@silvia-odwyer/photon` 0.3.3 for durable, `@silvia-odwyer/photon-node` 0.3.4 for the coding agent), whose wasm embeds image 0.24.9, gif 0.13.1, weezl 0.1.8 and png 0.17.14.

## Status

| item | status |
|---|---|
| BMP routing | done: `DecodeBMP` (`bmpdecode.go`, the ICO-mode port generalised), routed by `BM`; `golang.org/x/image/bmp` unregistered and banned by `decode_routing_test.go`; `decodeBitfieldBMP` deleted |
| GIF routing | done: `DecodeGIF` (`gifdecode.go`, `gifdecode_lzw.go`), routed by `GIF87a`/`GIF89a`; `image/gif` unregistered and banned |
| PNG routing | done: `DecodePNG` (`pngdecode.go`, `pngdecode_inflate.go`: the png 0.17.14 streaming decoder and reader, fdeflate 0.3.6, image 0.24.9 `PngDecoder` and `to_rgba8`), routed by `\x89PNG\r\n\x1a\n`; the ICO-only reader `icopng.go` is deleted and ICO entries use the same reader; `image/png` is no longer registered by `photonimage` and `decode_routing_test.go` bans it (the blank import, `image.Decode`, `png.Decode`) in every production file, `decode.go` included |
| zero-size durable case | done: Pi prepares an image with a width or a height of zero under a type providers do not take inline (see Zero-size findings); `Prepare` does the same; `EncodePNG` returns `ErrPNGNoPixels` where Photon's `get_bytes` throws |

## Confirmation per format

| format | Go decoder against Photon on the corpus | PiG |
|---|---|---|
| BMP | 10280 files, 5371 decode in Photon, 4909 throw. Routed through Go the test fails 4630 files (2598 files Photon decodes are refused by `x/image/bmp`, plus image types that are not NRGBA). `TestGoBMPDecoderDiffersFromPhoton`: refuses 2598 that Photon decodes, accepts 2 that Photon refuses, other pixels for 3 (184 files that claim more than 2^28 pixels are not given to Go's decoder) | ported, 0 differences of 10280 |
| GIF | 4904 files, 2677 decode in Photon, 2227 throw. Routed through Go the test fails 2715 files. `TestGoGIFDecoderDiffersFromPhoton`: refuses 653 that Photon decodes, accepts 37 that Photon refuses, other pixels for 135 | ported, 0 differences of 4904 |
| PNG | 8559 files, 3089 decode in Photon, 5470 throw. Routed through Go the test fails 2741 files. `TestGoPNGDecoderDiffersFromPhoton`: refuses 1523 that Photon decodes, accepts 34 that Photon refuses, other pixels for 285 (6578 identical; 139 files with more than 2^26 pixels are not given to Go's decoder) | ported, 0 differences of 8559; the ICO corpus (PNG entries) stays at 0 of 2241 |

## (a) upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/coding-agent/src/utils/image-convert.ts, clipboard-image.ts | ported (BMP, GIF) | internal/photonimage/{bmpdecode,gifdecode,gifdecode_lzw}.go through `photonimage.Decode`; internal/imageprocessing/decode_image.go `DecodeImage` | test:internal/photonimage/bmpdecode_oracle_test.go#TestDecodeBMPMatchesPhoton, test:internal/photonimage/gifdecode_oracle_test.go#TestDecodeGIFMatchesPhoton, test:internal/imageprocessing/decode_image_test.go#TestDecodeImageMatchesPhotonOnMalformedBitfieldBMP | a BMP or GIF is decoded or refused as Photon does |
| packages/coding-agent/src/utils/{image-convert,clipboard-image,image-resize-core,image-process}.ts, packages/durable/src/images/index.ts | ported (PNG) | internal/photonimage/{pngdecode,pngdecode_inflate}.go through `photonimage.Decode` | test:internal/photonimage/pngdecode_oracle_test.go#TestDecodePNGMatchesPhoton, test:internal/imageprocessing/process_image_png_oracle_test.go#TestProcessImagePNGMatchesPi, test:durable/images/photon_pi_formats_test.go#TestPhotonImagesMatchPiOnPNGFiles | a PNG is decoded or refused as Photon does, and the processed bytes are Pi's for every declared type |
| packages/durable/src/images/index.ts | ported (zero-size) | durable/images/images.go `Prepare`, internal/photonimage/png.go `EncodePNG` | test:durable/images/photon_pi_oracle_test.go#TestPhotonImagesMatchPiOnACorpus (the `emptyImageOracleCases`), test:internal/imageprocessing/process_image_png_oracle_test.go#TestProcessImageWithNoPixelsMatchesPi | an image with no pixels is prepared, encoded or refused as Pi does |

## (b) PORT_MAP rows

| Rust source (crate, path:lines) | Go | note |
|---|---|---|
| image 0.24.9 src/codecs/bmp/decoder.rs:1-1330 (`BmpDecoder::new`, `new_with_ico_format`, `read_metadata`, `read_file_header`, palette, RLE4/8, 16/24/32-bit, bitfields) | internal/photonimage/bmpdecode.go `DecodeBMP`, `bmpDecoder` | one decoder for the file and the ICO form: the file header, RGB8 output (alpha only through an alpha mask, `RGB32`), the 3-channel overflow check and limit; the four-byte buffer gets alpha 255 at the end when there is no alpha channel |
| image 0.24.9 src/codecs/gif.rs:1-330 (`GifDecoder::new`, `read_image`) | internal/photonimage/gifdecode.go `DecodeGIF` | the first frame placed on the logical screen, the frame buffer limit (512 MiB minus the screen) and the wasm32 `usize` checks |
| gif 0.13.1 src/reader/{mod.rs,decoder.rs,converter.rs} | internal/photonimage/gifdecode.go `gifStream`, `gifInterlace`, `readIntoBuffer` | the byte state machine (the empty-extension quirk, the control extension rules, the 50 MB extension limit), RGBA conversion, interlace order, `odd-sized buffer` for an empty line, and the 8 KiB `BufReader` window that decides whether the code after the last pixel is read |
| weezl 0.1.8 src/decode.rs:557-1233 (`DecodeState::advance`, `LsbBuffer`, `Table`) with `Decoder::new(Lsb, min_code_size)` | internal/photonimage/gifdecode_lzw.go `gifLZW` | the sequential form of the burst decoder: a word that does not fit is kept whole, a full output reads the next code, a clear code ends the call |
| png 0.17.14 src/decoder/{stream.rs:1-1569 (`StreamingDecoder::update`, `next_state`, `parse_u32`, `parse_chunk`, `parse_ihdr`, `parse_plte`, `parse_trns`, `parse_phys`, `parse_chrm`, `parse_gama`, `parse_srgb`, `parse_iccp`, `parse_fctl`, `reserve_current_chunk`, `Info::validate`), mod.rs:1-775 (`Decoder`, `ReadDecoder`, `Reader::read_until_image_data`, `next_frame`, `next_interlaced_row`, `next_raw_interlaced_row`, `output_color_type`, `SubframeInfo`), zlib.rs:1-230 (`ZlibStream`), transform.rs, transform/palette.rs, interlace_info.rs}, src/{adam7.rs, filter.rs (unfilter), common.rs (`Info`, `ColorType`)} | internal/photonimage/pngdecode.go `pngStream`, `pngReader`, `pngZlib`, `pngOpen`, `pngCreateTransform`, `pngRGBAPalette`, `pngInterlaceIter`, `pngExpandPass`, `pngUnfilter` | a step by step port: the chunk state machine (bytes taken from a 32 KiB `BufReader` window), the limits in the `usize` of wasm32 (chunk buffer growth by `reserve_exact`, PLTE, tRNS, the ICC profile and the output row are taken from `Limits::bytes`), the checksum rules (critical chunks verified, ancillary ones with a wrong checksum skipped), the header of the chunk after the image data that has to be there, EXPAND for every colour type and depth, the default image of an APNG, panics of the crate as errors |
| fdeflate 0.3.6 src/{decompress.rs:1-1054, huffman.rs:1-182, tables.rs:1-156} | internal/photonimage/pngdecode_inflate.go `fdDecompressor`, `fdBuildTable` | a literal port of the table-driven inflater (not compress/flate): the fast and the careful loops, the queued runs, the tables that are rebuilt over the ones of the last block, the end-of-block symbols 256, 286 and 287, the distance tree of one code, the zlib header rules, `decompress_to_vec_bounded` for the ICC profile |
| image 0.24.9 src/codecs/png.rs:119-199 (`PngDecoder::with_limits`), io/free_functions.rs:90-110 (`load_inner`: `Limits::default()` 512 MiB, `reserve(total_bytes)`), dynimage.rs `to_rgba8`, color.rs:408-421, 577-633 | internal/photonimage/pngdecode.go `DecodePNG`, `pngToRGBA8` | L8, La8, Rgb8, Rgba8 and the 16-bit types through `(v + 128) / 257`; the image buffer lengths in wasm32's `usize` and the 2 GiB `vec!` limit of `to_rgba8` |

## (c) upstream tests

None ported: Photon and the Rust crates have no Pi test here. New oracles against Photon (photon 0.3.3 and photon-node 0.3.4 agree on every file; `testdata/decodeformats_oracle.mjs`): `TestDecodeBMPMatchesPhoton` (10280 files; 52 real files from image-rs's BMP Suite copy in `testdata/bmp-real`), `TestDecodeGIFMatchesPhoton` (4904 files; 16 real files in `testdata/gif-real`). PNG: `TestDecodePNGMatchesPhoton` (8559 files: every colour type, depth, interlacing and filter; tRNS in 480+ shapes; chunk order and contents; APNG; Go's deflate at every level and split into IDAT chunks around the 32 KiB window; 100+ hand-written deflate blocks (fixed, stored, dynamic with trees of every shape, the symbols 286 and 287); 2400 seeded zlib damages; every prefix and every flipped byte of 8 small files; bad checksums of every chunk; 2000 seeded file damages; limits (dimensions, 130 to 260 MiB chunks, 500 MiB ICC profiles that take the limit, rows that fit or do not fit what is left); the 176 files of PngSuite, 18 of image-rs, 18 of Go and the Photon scene; `testdata/pngdecode_oracle.mjs`, `photon-pngdecode-corpus.tsv`; photon 0.3.3 and photon-node 0.3.4 agree on every file). Mutation checks of the port against the corpus: the BufReader window at 64 KiB is not seen (0 files: the outcome does not depend on how the stream is fed), the chunk buffer growth without the `saturating_sub` fails 3 files, symbols 286 and 287 as invalid codes 904, the ICC profile not taken from the limit 8, ancillary checksum errors 3 (the first of the 40 shown), a zlib stream without the adler32 rule 962, a finish at the end of the file 188, an RGB16 tRNS compared by prefix 23, the first chunk buffer of 64 KiB 1, the average filter 2163.

Mutation checks of the GIF port against the corpus: removing the `BufReader` window fails 1 file (`window-invalid-after-last-pad78`), removing the read of the code after a full output fails the interlaced files.

## (d) async contracts

None: the decoders are synchronous and pure.

## (e) interface IDs

None public. `photonimage.Decode`, `DecodeBMP` and `DecodeGIF` are in an internal package.

## Zero-size findings (Pi's answers, Photon 0.3.3 and photon-node 0.3.4 agree)

Probe under Node of `PhotonImage` on farbfeld files of 0x4, 4x0 and 0x0 (header only), and over Pi's own `images/index.ts` through the durable oracle:

- `new_from_byteslice` decodes them: width and height as declared, 0 bytes of pixels. A GIF with an interlaced frame of height 0 gives the same.
- `get_bytes()` (PNG) THROWS (`RuntimeError: unreachable`: the png crate's `ZeroWidth`/`ZeroHeight`) for all three. `get_bytes_jpeg(q)` does not throw: 625 bytes for every size and quality (header, no scan data), except where a side is over 65535, where it throws. `new PhotonImage(new Uint8Array(0), w, h)` THROWS. `fliph`, `flipv` and `resize` accept the empty image; `resize(empty, w, h)` is w x h of transparent black bytes (and an empty image for a zero target).
- durable `prepare`: under a type providers take inline (`image/png`, `image/jpeg`, `image/gif`, `image/webp`) with orientation 1 and the limits met, the bytes are returned as they are (`image.get_width() <= maxWidth` holds for 0). Under any other type (`image/x-icon`, `application/octet-stream`, `image/bmp`) or beyond a limit: `scale = Math.min(1, maxWidth / 0 = Infinity, maxHeight / 4)`, `target = { width: Math.max(1, Math.round(0)) = 1, height: ... }`, the image is resized to the target (differs from the empty size, so `resize` runs) and the PNG, or the JPEGs, of that 1xN transparent image are written: Pi answers prepared, with `resized: { from: 0x4, to: 1x4 }` and the PNG of 1x4 zero pixels. The loop shrinks as for any image when the byte limit is not met, and answers undefined at 1x1. An exception of an encoder (a JPEG side over 65535 in a resized image) leaves `prepare` through its catch. Orientation 5 to 8 would make `new PhotonImage` of 0 bytes throw: undefined (not reachable for farbfeld or GIF, which carry no EXIF).
- coding agent: `resizeImageInProcess`: within the limits and below the byte limit the bytes are returned as they are (`wasResized: false`); beyond them `resize(0xN, 0xM)` works and `get_bytes()` throws: null ("could not be resized"). `convertImageBytesToPng` (unsupported declared types, the clipboard, terminal conversion) throws on `get_bytes()`: null. PiG's `ProcessImage`, `ResizeImage` and the conversion already answered so; they are pinned by `TestProcessImageWithNoPixelsMatchesPi` (9 inputs, 8 declared types, with and without resize, two limits).
- PiG's `Prepare` returned nothing for the first case (red: 126 of 245 `emptyImageOracleCases` fail); it now matches Pi byte for byte, `EncodePNG` returns `ErrPNGNoPixels` for the empty image itself, `ResizeLanczos3` of an empty source already gave transparent black, and `EncodeJPEG` of an empty image is Photon's 625-byte file.

## Measurements

Red commits: BMP `891e25d8ad` (corpus and table with Decode giving the files to `x/image/bmp`: 4630 of 10280 fail; run with a scratch guard that refuses files claiming more than 2^28 pixels, because Go's decoder allocates the image from the header and the unguarded run exceeded 140 GB), GIF `eb4ea299c8` and `9633eab3bb` (2715 of 4904 fail). Green: `cbc986b4ca`, 0 differences in both.

PNG: red `3bdbc77b5b` (the corpus and its table with `Decode` giving PNG files to Go's `image/png`, with a scratch guard that refuses images of more than 2^26 pixels, which Go allocates from the header: `TestDecodePNGMatchesPhoton` fails 2741 of 8559 files, `TestGoPNGDecoderDiffersFromPhoton` refuses 1523, accepts 34, other pixels for 285). Green `6445df7c9d`: 0 differences in 8559 files and in the 2241 ICO files. Zero-size red `2a14b3c41a` (durable: 126 of 245 empty-image cases fail, `Prepare` returns nothing where Pi prepares), green `e77f039733`. The imageprocessing oracles (`TestProcessImagePNGMatchesPi`: the PNG files and damaged copies under seven declared types, resize on and off, two limits; `TestProcessImageWithNoPixelsMatchesPi`) and `TestPhotonImagesMatchPiOnPNGFiles` pass on both sides of the port (the coding agent paths already refused empty images; the PNG files that Go's decoder and Photon's reader treat alike are the same, the damaged ones differ only with Go's decoder, which `decode.go` no longer uses).

Decode benchmark (`decodeimages_bench_test.go`, xd670, `-cpu 1`, minimum of 3 runs of 3; Go's decoders return Paletted, Gray16 or NRGBA images and skip the RGBA conversion Photon's ports include):

| picture | Photon port ns/op | B/op | allocs/op | Go decoder ns/op | B/op | allocs/op |
|---|---|---|---|---|---|---|
| PNG RGBA8 1920x1080 | 27,970,556 | 16,039,637 | 1173 | 41,477,665 | 8,381,290 | 580 |
| PNG RGBA8 Adam7 640x360 | 4,272,681 | 2,216,773 | 715 | 4,938,529 | 1,972,904 | 503 |
| PNG paletted 1920x1080 | 16,579,445 | 9,587,749 | 1120 | 7,069,730 | 2,132,634 | 24 |
| PNG gray16 1920x1080 | 20,612,434 | 13,965,013 | 1150 | 31,079,806 | 4,311,568 | 1788 |
| PNG RGBA8 4000x3000 | 104,549,316 | 85,397,509 | 3201 | 153,085,605 | 48,171,274 | 2907 |
| BMP 32-bit 1920x1080 | 8,557,314 | 8,298,560 | 2 | 5,421,332 | 8,305,776 | 5 |
| GIF 256 colours 1920x1080 | 18,567,625 | 10,406,656 | 5 | 13,845,914 | 2,109,922 | 267 |

The PNG port is faster than Go's decoder where the output is RGBA (the inflater's literal pairs and the 8-byte refill); the palette expansion (2.3x Go's, which returns the indices) and BMP and GIF (1.3 to 1.6x Go's, which return indices or a different pixel layout) are not notably slower and are not optimised.

Binary size (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" ./examples/headless`, the perfgate `binary.headless.default.linux_amd64.bytes`): base `ba33bfe21a` 36,933,792 bytes, head 36,982,944 (+49,152; `image/png` stays linked through the encoders and the tests' own uses, the two PNG ports come in). The committed ceiling is 36,741,120: base is 192,672 over it and head 241,824 over, so `TestHeadlessEmbedBinarySizeGate/default_linux_amd64` fails on both and needs the owner-approved move (not edited here).

Files touched (PNG and zero-size): internal/photonimage/{pngdecode,pngdecode_inflate,decode,icodecode,png}.go (icopng.go deleted), tests pngdecode_corpus_test.go, pngdecode_oracle_test.go, decodeimages_bench_test.go, empty_image_test.go, icodecode_png_corpus_test.go, decode_routing_test.go, png_test.go, testdata/{pngdecode_oracle.mjs,photon-pngdecode-corpus.tsv,png-real}; durable/images/{images.go,photon_pi_formats_test.go}; internal/imageprocessing/{images.go,process_image_formats_oracle_test.go,process_image_png_oracle_test.go}; REUSE.toml; LICENSES/LicenseRef-PngSuite.txt; THIRD_PARTY_NOTICES.md; changelog.d/photon-png-decode-6445df7c9d.md, photon-empty-image-e77f039733.md. BMP and GIF: internal/photonimage/{bmpdecode,gifdecode,gifdecode_lzw,decode,icodecode}.go, their tests (`bmpdecode_corpus_test.go`, `bmpdecode_oracle_test.go`, `gifdecode_corpus_test.go`, `gifdecode_oracle_test.go`, `decode_routing_test.go`), testdata/{photon-bmpdecode-corpus.tsv,photon-gifdecode-corpus.tsv,bmp-real,gif-real}; internal/imageprocessing/{decode_image.go (was bmp_bitfields.go),decode_image_test.go}; REUSE.toml; THIRD_PARTY_NOTICES.md; changelog.d/photon-decode-e-*.md.
