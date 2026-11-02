# Lane photon-decode-d rows (Photon's TIFF, ICO, PNM, DDS, Radiance HDR and farbfeld decoders), branch fix/follow-photon-decode-d

Base `8881071411` (`fix/follow-pi-next` with `fix/follow-photon-decode-c` merged, Pi `e7a0df27`). No Pi source file changes: the Rust that Pi runs is Photon (`@silvia-odwyer/photon` 0.3.3 for durable, `@silvia-odwyer/photon-node` 0.3.4 for the coding agent), whose wasm embeds image 0.24.9 with the features `gif,jpeg,ico,png,pnm,tga,tiff,webp,bmp,hdr,dxt,dds,farbfeld` (photon-rs 0.3.3 `Cargo.toml` `[dependencies.image]`; there is no photon-rs 0.3.4 crate: the node package 0.3.4 embeds the same crates, `strings photon_rs_bg.wasm` of both lists the same `image-0.24.9\src\codecs\...` panic paths, `png-0.17.14`, `tiff-0.9.1`, `jpeg-decoder-0.3.1`, `weezl-0.1.8`). Pi decodes with `PhotonImage.new_from_byteslice` = `image::load_from_memory(bytes).unwrap()` then `to_rgba8()` (photon-rs `src/lib.rs:93-105`) on the clipboard, in `processImage` (declared types other than PNG, JPEG, GIF and WebP through `convertImageBytesToPng`; the bytes of those four through `resizeImage`, which guesses by bytes), in the Kitty transcoder and in durable `images/index.ts`.

## Confirmation per format

| format | linked in the wasm | feature on | `guess_format` signature (image 0.24.9 `io/free_functions.rs:266-290`) | reachable | PiG |
|---|---|---|---|---|---|
| TIFF | `tiff-0.9.1\src\decoder\*`, weezl-0.1.8 | tiff | `MM\0*`, `II*\0` (:272-273) | yes | ported (branch -c): `DecodeTIFF` |
| ICO | `image::codecs::ico::decoder::read_entry`, `png-0.17.14` | ico | `00 00 01 00` (:276); `load_decoder` :72-73 | yes | ported: `DecodeICO` over `icoBMP` and the png reader |
| PNM | `pnm::decoder::*`, `pnm::header::*` | pnm | `P1`..`P7` (:278-284) | yes | ported: `DecodePNM` |
| DDS (+DXT) | `image::codecs::dds::*`, `image::codecs::dxt::DxtDecoder` | dds, dxt | `DDS ` (:274) | yes (DXT only through DDS) | ported: `DecodeDDS` (DXT1, DXT3, DXT5; BC1 to BC3 through the DX10 header) |
| HDR | `image::codecs::hdr::decoder::HdrAdapter`, `libm powf` | hdr | `#?RADIANCE` (:277) | yes | ported: `DecodeHDR` |
| farbfeld | `image::codecs::farbfeld::FarbfeldDecoder` | farbfeld | `farbfeld` (:285) | yes | ported: `DecodeFarbfeld` |
| TGA | `tga::decoder::TgaDecoder` | tga | none: "TGA is not supported by this function" (:295) | no: `guess_format` never answers Tga, and `load_from_memory` has no other route | none needed; `Decode` answers `image: unknown format` as Photon throws |
| AVIF, OpenEXR, QOI | not linked (`avif-decoder`, `exr`, `qoi` features off; only the `ftypavif` sniff strings exist) | off | `ftypavif`, `v/1\x01`, `qoif` (:286-289) | the format is guessed, `load_decoder` answers `Unsupported` | none needed (an error both ways) |
| BMP, GIF, PNG | linked | on | `BM`, `GIF8[79]a`, `\x89PNG..` | yes | unchanged (stdlib decoders, proven by earlier lanes); BMP in ICO needs image's BMP decoder in ICO mode, ported as `icoBMP` |

`to_rgba8` (image `dynimage.rs`, `color.rs:379-421,579-633`): L8/Rgb8 gain alpha 255, Rgba8 is copied, 16-bit samples become `(c + 128) / 257`; HDR is `Rgb8` (not float): `Rgbe8Pixel::to_ldr` = `powf(exp2f(e - 136) * c, 2.2) * 255 + 0.5` truncated (`hdr/decoder.rs:266-325`, libm `powf`/`exp2f` from compiler_builtins).

## (a) upstream files

| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/coding-agent/src/utils/clipboard-image.ts | ported | internal/photonimage/{tiff,ico,pnm,dds,hdr,farbfeld}decode*.go through `photonimage.Decode`; internal/imageprocessing/bmp_bitfields.go `DecodeImage` | test:internal/codingagent/clipboard_normalization_test.go#TestClipboardImageNormalization (TIFF, ICO, DDS, PNM, HDR and farbfeld targets become PNG; damaged ones are absent) | the clipboard targets Photon decodes are converted as Pi converts them |
| packages/coding-agent/src/utils/image-process.ts, image-convert.ts | ported | internal/imageprocessing/images.go `ProcessImage` (an empty image is a PNG encoding error, as the png crate's `ZeroWidth` makes `get_bytes().unwrap()` throw) | test:internal/imageprocessing/process_image_tiff_oracle_test.go#TestProcessImageTIFFMatchesPi (900 cases), test:internal/imageprocessing/process_image_formats_oracle_test.go#TestProcessImageDecodedFormatsMatchPi (29 files under 5 declared types, resize on and off, two limits) | SHA-256 of the returned bytes, type, hints and message equal Pi's |
| packages/durable/src/images/index.ts | ported | durable/images/images.go (empty image refused by the PNG candidate), internal/photonimage | test:durable/images/photon_pi_oracle_test.go#TestPhotonImagesMatchPiOnACorpus (TIFF files and the new formats as image/png, image/tiff or image/x-icon and beyond a small limit) | prepared / not prepared, format, resized, convertedFrom and every byte equal Pi's |

## (b) PORT_MAP rows

| Rust source (crate, path:lines) | Go | note |
|---|---|---|
| image 0.24.9 src/io/free_functions.rs:266-312 `MAGIC_BYTES`, `guess_format`; dynimage.rs `decoder_to_image`, `to_rgba8`; io/mod.rs:63-180 `Limits` | internal/photonimage/decode.go `Decode`; decodeformats.go `reserveTotalBytes`, `checkRGBAConversion`, `downscale16`, `rustParseUint` | the routing in guess_format's order, the 512 MiB `Limits::default().max_alloc`, and the `ImageBuffer` length arithmetic of wasm32's 32-bit `usize` (`buffer.rs:807-811`: an empty image of width >= 2^30 panics in `to_rgba8`) |
| tiff 0.9.1 src/decoder/{mod.rs,image.rs,ifd.rs,stream.rs}, tags.rs; image 0.24.9 codecs/tiff.rs:210-318 (`TiffDecoder`, `read_image`, `cmyk_to_rgb`) | internal/photonimage/tiffdecode.go `DecodeTIFF` | branch -c (c3dd9cc9f3 red, facd278025 production): IFD reading with its limits, strips and tiles, chunky and planar, 8/16-bit gray, RGB, RGBA, 8-bit CMYK, predictor |
| weezl 0.1.8 src/decode.rs; tiff decoder/stream.rs:111-157 `LZWReader` | internal/photonimage/tiffdecode_lzw.go | the sequential form of weezl's burst decoder, with when an invalid code is noticed |
| miniz_oxide 0.7.1 src/inflate/{core.rs,stream.rs:185-357}; flate2 1.0.28 src/zio.rs:118-150 | internal/photonimage/tiffdecode_inflate.go | tinfl with its 32 KiB dictionary read-ahead, for the error parity of damaged Deflate strips |
| jpeg-decoder 0.3.1 (raw output, tiff image.rs:376-427) | internal/photonimage/tiffdecode_jpeg.go | the JPEG branch of a TIFF |
| image 0.24.9 src/codecs/farbfeld.rs:38-216 | internal/photonimage/farbfelddecode.go `DecodeFarbfeld` | 16-bit big endian RGBA, `(v + 128) / 257` |
| image 0.24.9 src/codecs/hdr/decoder.rs:1-1000 (`HdrAdapter`, `HdrDecoder::with_strictness`, `read_scanline`, `decode_component`, `decode_old_rle`, `Rgbe8Pixel::to_ldr`, `parse_dimensions_line`, `HdrMetadata::update_header_info`) | internal/photonimage/hdrdecode.go `DecodeHDR` | strict mode; Rust's `str::parse` grammars for the header numbers; the old-style run multiplier wraps in `usize` of wasm32 |
| libm 0.2.16 src/math/{powf.rs,exp2f.rs} (FreeBSD e_powf.c, s_exp2f.c; Photon's wasm: compiler_builtins `libm::powf::powf`) | internal/photonimage/libmf_reference_test.go (reference); production uses `math.Pow` | `TestHDRConversionTableIsLibms`: all 65,536 (exponent, mantissa) pairs, the whole domain of the decoder, give the same byte with `math.Pow` as with the literal port |
| image 0.24.9 src/codecs/pnm/{decoder.rs:1-1060,header.rs}, utils/mod.rs:37-63 `expand_bits` | internal/photonimage/pnmdecode.go `DecodePNM` | P1 to P7; the comment that glues numbers (`1#c\n2` is 12), the PAM header split at the position found in the untrimmed line, maxval scaling `round(v * target / maxval)` in f32 |
| image 0.24.9 src/codecs/dds.rs:81-356, dxt.rs:16-160,261-473 (`DxtDecoder`, `decode_dxt_colors`, `alpha_table_dxt5`, `decode_dxt{1,3,5}_row`) | internal/photonimage/ddsdecode.go `DecodeDDS` | DXT1/3/5 and the DX10 header's BC1 to BC3; the integer interpolation of the colour table |
| image 0.24.9 src/codecs/ico/decoder.rs:1-405 (`IcoDecoder`, `read_entries`, `best_entry`, the AND mask) | internal/photonimage/icodecode.go `DecodeICO` | the best entry by (bits per pixel, area), the last of equals; PNG and BMP entries |
| image 0.24.9 src/codecs/bmp/decoder.rs:1-1330 in ICO mode (`new_with_ico_format`, `read_metadata_in_ico_format`, palette, RLE4/8, 16/24/32-bit, bitfields) | internal/photonimage/icodecode_bmp.go `icoDecodeBMP` | no file header, RGBA output, doubled height; plain BMP files keep the stdlib route |
| png 0.17.14 src/decoder/{stream.rs:625-1430,mod.rs:100-745,zlib.rs,transform.rs,transform/palette.rs}, src/adam7.rs; fdeflate 0.3.7 decompress.rs zlib header and checksum rules; image 0.24.9 codecs/png.rs:99-304 | internal/photonimage/icopng.go `icoPNGReadInfo`, `icoPNGDecode` | what the crate reads of the PNG an ICO entry holds: ancillary checksum failures skipped, empty chunks never parsed, tRNS rules, the IEND chunk never read, the zlib adler32 ignored but its four bytes required, fcTL and fdAT image data |

## (c) upstream tests

None ported or designed out: Photon and the Rust crates have no Pi test here, and Pi has no test of these decoders. New oracles against Photon (Photon 0.3.3 and photon-node 0.3.4 agree on every file; `testdata/decodeformats_oracle.mjs` writes the tables, `TestDecode<Format>MatchesPhoton` compares dimensions and the SHA-256 of the RGBA8 pixels, or an error, with a fresh Photon instance after every throw): `TestDecodeFarbfeldMatchesPhoton` (87 files), `TestDecodeHDRMatchesPhoton` (710), `TestDecodePNMMatchesPhoton` (1082), `TestDecodeDDSMatchesPhoton` (1053), `TestDecodeICOMatchesPhoton` (2241), `TestDecodeTIFFMatchesPhoton` (9889, branch -c, real files from macOS sips and libtiff in `testdata/tiff-real`), with the unit tests of each decoder and `TestDecodeRoutes*`. Pi oracles listed in (a).

## (d) async contracts

None: the decoders are synchronous and pure.

## (e) interface IDs

None public. `photonimage.Decode(data) (image.Image, error)` is in an internal package; `internal/imageprocessing.DecodeImage` and `durable/images.Prepare` call it.

## Measurements

Red (corpus and table committed with `Decode` not routing the format; every file Photon decodes fails with `image: unknown format`, the files Photon throws on already match):

| format | red commit | files | Photon decodes | red failures | green |
|---|---|---|---|---|---|
| TIFF (branch -c) | c3dd9cc9f3 | 9822 (9889 with the real files) | 3281 | 3281 | 0 differences of 9889 |
| farbfeld | abe9d44ed1 | 87 | 23 | 23 | 0 of 87 |
| HDR | 2fdadd9d85 | 710 | 192 | 192 | 0 of 710 |
| PNM | 73ec1727a2 | 1082 | 656 | 656 | 0 of 1082 |
| DDS | 39024cb057 | 1053 | 162 | 162 | 0 of 1053 |
| ICO | cba5280fca, 5692c0aee7 (the PNG streams) | 2241 | 922 | 922 | 0 of 2241 |

The first ICO attempt (BMP port plus Go's `image/png` for the entry) left 21 of 1932 files different, all PNG: a tRNS chunk longer than the palette (the crate ignores it), an empty tRNS (never parsed), the IEND chunk (never read), and flips in ancillary chunks (checksum failures skipped); the png reader emulation (`icopng.go`) and 309 files of crafted streams (every filter, Adam7, split and empty IDAT chunks, zlib damage, chunk order, APNG) closed it: 4 of 2241 then differed (ancillary checksums, fdAT image data) and were fixed from `stream.rs:441,875-880` and `:819-834`. Pi oracles on the final head: `TestProcessImageDecodedFormatsMatchPi` (29 files x 5 types x 4 option sets), `TestPhotonImagesMatchPiOnACorpus`, `TestClipboardImageNormalization`: 0 failures. A zero-pixel image reached the PNG encoder (farbfeld 0x4): it panicked, and Pi's `get_bytes()` throws (the png crate's `ZeroWidth`); `encodePNG` now returns an error.

Findings that are not decoder bugs: an L8 image of exactly 2^29 bytes (PNM `P5 32768x16384`) is refused by Photon's wasm for memory (3 GiB live), which Decode does not emulate and the corpus omits (as for TIFF); the L16 image of 2^29 bytes decodes and one row more is refused. `math.Pow` equals the libm `powf` on the whole HDR domain, so the libm port is kept only as the reference test.

Decode benchmark (`BenchmarkDecodeFormats`, xd670 Xeon 8468, GOMAXPROCS 1, best of 3 runs of 5, no Go baseline exists: the standard library has none of these decoders): ICO 256x256 32-bit 0.65 ms (415 MB/s); PNM P6 1920x1080 7.2 ms (864 MB/s, 14.5 MB); PNM P3 ASCII 640x360 16.8 ms; DDS DXT5 1920x1080 14.1 ms (147 MB/s); DDS DXT1 1920x1080 11.6 ms; Radiance HDR 1920x1080 16.8 ms (435 MB/s); farbfeld 1920x1080 17.5 ms (950 MB/s). Allocations: 2 to 6 per decode (the pixels plus a row).

Binary size (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" ./examples/headless`, the perfgate `binary.headless.default.linux_amd64.bytes`): 36,761,760 (`75a01fd0d2`, before TIFF) to 36,835,488 (`8881071411`, TIFF: +73,728) to 36,933,792 (this head: +98,304 for ICO, PNM, DDS, HDR, farbfeld; +172,032 together). The committed ceiling in `automation/perf/ratchet.toml` is 36,741,120: `75a01fd0d2` already exceeded it by 20,640, `8881071411` by 94,368 and this head by 192,672, so `TestHeadlessEmbedBinarySizeGate/default_linux_amd64` fails; the ceiling needs an owner-approved move (not edited here; rounded up to 8 KiB: 36,937,728).

Files touched: internal/photonimage/{decode,decodeformats,farbfelddecode,hdrdecode,pnmdecode,ddsdecode,icodecode,icodecode_bmp,icopng}.go and their tests (`*_corpus_test.go`, `*_oracle_test.go`, `*_test.go`, `decodeformats_oracle_test.go`, `decodeformats_bench_test.go`, `libmf_reference_test.go`), testdata/{decodeformats_oracle.mjs,photon-{farbfeld,hdr,pnm,dds,ico}decode-corpus.tsv}; internal/imageprocessing/images.go and process_image_formats_oracle_test.go; durable/images/images.go, photon_pi_formats_test.go, photon_pi_oracle_test.go; internal/codingagent/clipboard_formats_fixture_test.go, clipboard_normalization_test.go; THIRD_PARTY_NOTICES.md, LICENSES/BSD-2-Clause.txt; changelog.d/photon-decode-d-*.md.
