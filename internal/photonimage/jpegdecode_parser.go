// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"errors"
	"fmt"
)

// Ports jpeg-decoder 0.3.1 (crates.io jpeg-decoder-0.3.1.crate; Photon's wasm embeds exactly this version, its panic paths name
// jpeg-decoder-0.3.1\src\decoder.rs, huffman.rs, parser.rs, upsampler.rs, worker\mod.rs and decoder\lossless.rs) and the JPEG wrapper of
// image 0.24.9 (src/codecs/jpeg/decoder.rs). Photon's `PhotonImage.new_from_byteslice` is `image::load_from_memory(bytes).unwrap()`
// followed by `to_rgba8()` (photon-rs 0.3.3 lib.rs:93-105); DecodeJPEG returns those RGBA pixels, and an error exactly where the
// `unwrap` panics. This file is parser.rs and marker.rs (cited as parser.rs and marker.rs); decoder.rs is jpegdecode.go, huffman.rs
// jpegdecode_huffman.go, idct.rs and worker/immediate.rs jpegdecode_idct.go, upsampler.rs and the colour conversions of decoder.rs
// jpegdecode_upsample.go, and decoder/lossless.rs jpegdecode_lossless.go. The wasm build has no simd128 (its target_features section
// is multivalue, mutable-globals, reference-types and sign-ext), so the arch-specific IDCT and colour conversion of arch/ are not
// compiled in; the worker is the ImmediateWorker (worker/mod.rs:46-95 cfg's every other worker out on wasm32); and overflow checks
// are off (a release build: the wasm has no "attempt to ... with overflow" panic string except one in the standard library), so
// integer arithmetic wraps.

// jpegMaxComponents is decoder.rs:21 MAX_COMPONENTS.
const jpegMaxComponents = 4

// errJPEGEOF is `Error::Io(UnexpectedEof)`: the input ended inside a read.
var errJPEGEOF = errors.New("photonimage: jpeg: unexpected end of file")

// jpegFormatError is `Error::Format`.
func jpegFormatError(format string, args ...any) error {
	return fmt.Errorf("photonimage: invalid JPEG format: "+format, args...)
}

// jpegUnsupported is `Error::Unsupported`.
func jpegUnsupported(feature string) error {
	return fmt.Errorf("photonimage: unsupported JPEG feature: %s", feature)
}

// jpegReader is the `Cursor<&[u8]>` the Rust decoder reads from.
type jpegReader struct {
	data []byte
	pos  int
}

// readU8 is lib.rs:56-60 read_u8.
func (r *jpegReader) readU8() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, errJPEGEOF
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

// readU16BE is lib.rs:62-66 read_u16_from_be.
func (r *jpegReader) readU16BE() (uint16, error) {
	if len(r.data)-r.pos < 2 {
		r.pos = len(r.data)
		return 0, errJPEGEOF
	}
	v := uint16(r.data[r.pos])<<8 | uint16(r.data[r.pos+1])
	r.pos += 2
	return v, nil
}

// readExact is `Read::read_exact`: the next n bytes (a view into the input), or an end-of-file error.
func (r *jpegReader) readExact(n int) ([]byte, error) {
	if len(r.data)-r.pos < n {
		r.pos = len(r.data)
		return nil, errJPEGEOF
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// skip is parser.rs:149-159 skip_bytes.
func (r *jpegReader) skip(n int) error {
	_, err := r.readExact(n)
	return err
}

// Marker bytes: marker.rs:64-135 Marker::from_u8. The Rust enum is kept as the byte; the classes below are its variants.
const (
	jpegMarkerTEM   = 0x01 // marker.rs:68
	jpegMarkerSOF0  = 0xC0
	jpegMarkerDHT   = 0xC4
	jpegMarkerDAC   = 0xCC
	jpegMarkerRST0  = 0xD0
	jpegMarkerRST7  = 0xD7
	jpegMarkerSOI   = 0xD8
	jpegMarkerEOI   = 0xD9
	jpegMarkerSOS   = 0xDA
	jpegMarkerDQT   = 0xDB
	jpegMarkerDNL   = 0xDC
	jpegMarkerDRI   = 0xDD
	jpegMarkerDHP   = 0xDE
	jpegMarkerEXP   = 0xDF
	jpegMarkerAPP0  = 0xE0
	jpegMarkerAPP15 = 0xEF
	jpegMarkerCOM   = 0xFE
)

// jpegIsSOF reports Marker::SOF(_): C0-CF except DHT (C4), JPG (C8) and DAC (CC).
func jpegIsSOF(m byte) bool {
	return m >= 0xC0 && m <= 0xCF && m != jpegMarkerDHT && m != 0xC8 && m != jpegMarkerDAC
}

func jpegIsRST(m byte) bool { return m >= jpegMarkerRST0 && m <= jpegMarkerRST7 }

// jpegMarkerHasLength is marker.rs:59-62 has_length for the markers read_length is called with.
func jpegMarkerHasLength(m byte) bool {
	return !jpegIsRST(m) && m != jpegMarkerSOI && m != jpegMarkerEOI && m != jpegMarkerTEM
}

type jpegDimensions struct{ width, height uint16 }

type jpegCodingProcess uint8

const (
	jpegDCTSequential jpegCodingProcess = iota
	jpegDCTProgressive
	jpegLossless
)

// jpegPredictor is the lossless predictor of Table H.1 (parser.rs:37-47).
type jpegPredictor uint8

const (
	jpegNoPrediction jpegPredictor = iota
	jpegPredRa
	jpegPredRb
	jpegPredRc
	jpegPredRaRbRc1
	jpegPredRaRbRc2
	jpegPredRaRbRc3
	jpegPredRaRb
)

// jpegComponent is parser.rs:76-90 Component.
type jpegComponent struct {
	identifier             uint8
	hSampling, vSampling   uint8
	quantizationTableIndex int
	dctScale               int
	size, blockSize        jpegDimensions
}

// jpegFrame is parser.rs:49-62 FrameInfo.
type jpegFrame struct {
	isBaseline     bool
	isDifferential bool
	codingProcess  jpegCodingProcess
	arithmetic     bool // entropy_coding == Arithmetic
	precision      uint8
	imageSize      jpegDimensions
	outputSize     jpegDimensions
	mcuSize        jpegDimensions
	components     []jpegComponent
}

// jpegScan is parser.rs:63-75 ScanInfo; spectral_selection is the Range start..end.
type jpegScan struct {
	componentIndices               [jpegMaxComponents]int
	dcTableIndices, acTableIndices [jpegMaxComponents]int
	count                          int
	spectralStart, spectralEnd     uint8
	predictor                      jpegPredictor
	successiveHigh, successiveLow  uint8
	pointTransform                 uint8
}

// jpegAdobeTransform is parser.rs:103-111 AdobeColorTransform.
type jpegAdobeTransform uint8

const (
	jpegAdobeUnknown jpegAdobeTransform = iota
	jpegAdobeYCbCr
	jpegAdobeYCCK
)

// readLength is parser.rs:136-147 read_length: the segment length without the two length bytes.
func (r *jpegReader) readLength(marker byte) (int, error) {
	if !jpegMarkerHasLength(marker) {
		return 0, errors.New("photonimage: jpeg: assertion failed: marker.has_length()")
	}
	length, err := r.readU16BE()
	if err != nil {
		return 0, err
	}
	if length < 2 {
		return 0, jpegFormatError("encountered marker 0x%02X with invalid length %d", marker, length)
	}
	return int(length) - 2, nil
}

// parseSOF is parser.rs:161-281 parse_sof.
func (r *jpegReader) parseSOF(marker byte) (*jpegFrame, error) {
	length, err := r.readLength(marker)
	if err != nil {
		return nil, err
	}
	if length <= 6 {
		return nil, jpegFormatError("invalid length in SOF")
	}

	n := marker - jpegMarkerSOF0
	isBaseline := n == 0
	isDifferential := (n >= 5 && n <= 7) || (n >= 13 && n <= 15)
	var coding jpegCodingProcess
	switch n {
	case 0, 1, 5, 9, 13:
		coding = jpegDCTSequential
	case 2, 6, 10, 14:
		coding = jpegDCTProgressive
	default: // 3, 7, 11, 15
		coding = jpegLossless
	}
	arithmetic := n >= 9

	precision, err := r.readU8()
	if err != nil {
		return nil, err
	}
	switch precision {
	case 8:
	case 12:
		if isBaseline {
			return nil, jpegFormatError("12 bit sample precision is not allowed in baseline")
		}
	default:
		if coding != jpegLossless || precision > 16 {
			return nil, jpegFormatError("invalid precision %d in frame header", precision)
		}
	}

	height, err := r.readU16BE()
	if err != nil {
		return nil, err
	}
	width, err := r.readU16BE()
	if err != nil {
		return nil, err
	}
	// "Value 0 indicates that the number of lines shall be defined by the DNL marker" (parser.rs:206-210).
	if height == 0 {
		return nil, jpegUnsupported("DNL")
	}
	if width == 0 {
		return nil, jpegFormatError("zero width in frame header")
	}

	componentCount, err := r.readU8()
	if err != nil {
		return nil, err
	}
	if componentCount == 0 {
		return nil, jpegFormatError("zero component count in frame header")
	}
	if coding == jpegDCTProgressive && componentCount > 4 {
		return nil, jpegFormatError("progressive frame with more than 4 components")
	}
	if length != 6+3*int(componentCount) {
		return nil, jpegFormatError("invalid length in SOF")
	}

	components := make([]jpegComponent, 0, componentCount)
	for range componentCount {
		identifier, err := r.readU8()
		if err != nil {
			return nil, err
		}
		for i := range components {
			if components[i].identifier == identifier {
				return nil, jpegFormatError("duplicate frame component identifier %d", identifier)
			}
		}
		b, err := r.readU8()
		if err != nil {
			return nil, err
		}
		hs, vs := b>>4, b&0x0f
		if hs == 0 || hs > 4 {
			return nil, jpegFormatError("invalid horizontal sampling factor %d", hs)
		}
		if vs == 0 || vs > 4 {
			return nil, jpegFormatError("invalid vertical sampling factor %d", vs)
		}
		quantizationTableIndex, err := r.readU8()
		if err != nil {
			return nil, err
		}
		if quantizationTableIndex > 3 || (coding == jpegLossless && quantizationTableIndex != 0) {
			return nil, jpegFormatError("invalid quantization table index %d", quantizationTableIndex)
		}
		components = append(components, jpegComponent{
			identifier:             identifier,
			hSampling:              hs,
			vSampling:              vs,
			quantizationTableIndex: int(quantizationTableIndex),
			dctScale:               8,
		})
	}

	mcuSize, err := jpegUpdateComponentSizes(jpegDimensions{width, height}, components)
	if err != nil {
		return nil, err
	}
	return &jpegFrame{
		isBaseline:     isBaseline,
		isDifferential: isDifferential,
		codingProcess:  coding,
		arithmetic:     arithmetic,
		precision:      precision,
		imageSize:      jpegDimensions{width, height},
		outputSize:     jpegDimensions{width, height},
		mcuSize:        mcuSize,
		components:     components,
	}, nil
}

// jpegCeilDiv is parser.rs:282-290 ceil_div: ceil(x/y), x > 0.
func jpegCeilDiv(x, y uint32) (uint16, error) {
	if x == 0 || y == 0 {
		return 0, jpegFormatError("invalid dimensions")
	}
	return uint16(1 + (x-1)/y), nil
}

// jpegUpdateComponentSizes is parser.rs:292-311 update_component_sizes.
func jpegUpdateComponentSizes(size jpegDimensions, components []jpegComponent) (jpegDimensions, error) {
	var hMax, vMax uint32
	for i := range components {
		hMax = max(hMax, uint32(components[i].hSampling))
		vMax = max(vMax, uint32(components[i].vSampling))
	}
	width, err := jpegCeilDiv(uint32(size.width), hMax*8)
	if err != nil {
		return jpegDimensions{}, err
	}
	height, err := jpegCeilDiv(uint32(size.height), vMax*8)
	if err != nil {
		return jpegDimensions{}, err
	}
	mcu := jpegDimensions{width, height}

	for i := range components {
		c := &components[i]
		c.size.width, err = jpegCeilDiv(uint32(size.width)*uint32(c.hSampling)*uint32(c.dctScale), hMax*8)
		if err != nil {
			return jpegDimensions{}, err
		}
		c.size.height, err = jpegCeilDiv(uint32(size.height)*uint32(c.vSampling)*uint32(c.dctScale), vMax*8)
		if err != nil {
			return jpegDimensions{}, err
		}
		c.blockSize.width = mcu.width * uint16(c.hSampling)
		c.blockSize.height = mcu.height * uint16(c.vSampling)
	}
	return mcu, nil
}

// parseSOS is parser.rs:332-477 parse_sos.
func (r *jpegReader) parseSOS(frame *jpegFrame) (*jpegScan, error) {
	length, err := r.readLength(jpegMarkerSOS)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, jpegFormatError("zero length in SOS")
	}
	componentCount, err := r.readU8()
	if err != nil {
		return nil, err
	}
	if componentCount == 0 || componentCount > 4 {
		return nil, jpegFormatError("invalid component count %d in scan header", componentCount)
	}
	if length != 4+2*int(componentCount) {
		return nil, jpegFormatError("invalid length in SOS")
	}

	scan := &jpegScan{count: int(componentCount)}
	for n := range scan.count {
		identifier, err := r.readU8()
		if err != nil {
			return nil, err
		}
		componentIndex := -1
		for i := range frame.components {
			if frame.components[i].identifier == identifier {
				componentIndex = i
				break
			}
		}
		if componentIndex < 0 {
			return nil, jpegFormatError("scan component identifier %d does not match any of the component identifiers defined in the frame", identifier)
		}
		highest := 0
		for _, c := range scan.componentIndices[:n] {
			if c == componentIndex {
				return nil, jpegFormatError("duplicate scan component identifier %d", identifier)
			}
			highest = max(highest, c)
		}
		// "... the ordering in the scan header shall follow the ordering in the frame header." (`unwrap_or(&0)`: 0 when empty)
		if componentIndex < highest {
			return nil, jpegFormatError("the scan component order does not follow the order in the frame header")
		}
		b, err := r.readU8()
		if err != nil {
			return nil, err
		}
		dcTableIndex, acTableIndex := b>>4, b&0x0f
		if dcTableIndex > 3 || (frame.isBaseline && dcTableIndex > 1) {
			return nil, jpegFormatError("invalid dc table index %d", dcTableIndex)
		}
		if acTableIndex > 3 || (frame.isBaseline && acTableIndex > 1) {
			return nil, jpegFormatError("invalid ac table index %d", acTableIndex)
		}
		scan.componentIndices[n] = componentIndex
		scan.dcTableIndices[n] = int(dcTableIndex)
		scan.acTableIndices[n] = int(acTableIndex)
	}

	blocksPerMCU := 0
	for _, i := range scan.componentIndices[:scan.count] {
		blocksPerMCU += int(frame.components[i].hSampling) * int(frame.components[i].vSampling)
	}
	if componentCount > 1 && blocksPerMCU > 10 {
		return nil, jpegFormatError("scan with more than one component and more than 10 blocks per MCU")
	}

	// Also utilized as 'Predictor' in lossless coding.
	spectralSelectionStart, err := r.readU8()
	if err != nil {
		return nil, err
	}
	spectralSelectionEnd, err := r.readU8()
	if err != nil {
		return nil, err
	}
	b, err := r.readU8()
	if err != nil {
		return nil, err
	}
	scan.successiveHigh, scan.successiveLow = b>>4, b&0x0f
	scan.pointTransform = scan.successiveLow

	switch frame.codingProcess {
	case jpegDCTProgressive:
		scan.predictor = jpegNoPrediction
		if spectralSelectionEnd > 63 || spectralSelectionStart > spectralSelectionEnd ||
			(spectralSelectionStart == 0 && spectralSelectionEnd != 0) {
			return nil, jpegFormatError("invalid spectral selection parameters: ss=%d, se=%d", spectralSelectionStart, spectralSelectionEnd)
		}
		if spectralSelectionStart != 0 && componentCount != 1 {
			return nil, jpegFormatError("spectral selection scan with AC coefficients can't have more than one component")
		}
		if scan.successiveHigh > 13 || scan.successiveLow > 13 {
			return nil, jpegFormatError("invalid successive approximation parameters: ah=%d, al=%d", scan.successiveHigh, scan.successiveLow)
		}
		// Section G.1.1.1.2
		if scan.successiveHigh != 0 && scan.successiveHigh != scan.successiveLow+1 {
			return nil, jpegFormatError("successive approximation scan with more than one bit of improvement")
		}
	case jpegLossless:
		if spectralSelectionEnd != 0 {
			return nil, jpegFormatError("spectral selection end shall be zero in lossless scan")
		}
		if scan.successiveHigh != 0 {
			return nil, jpegFormatError("successive approximation high shall be zero in lossless scan")
		}
		if spectralSelectionStart > 7 {
			return nil, jpegFormatError("invalid predictor selection value: %d", spectralSelectionStart)
		}
		scan.predictor = jpegPredictor(spectralSelectionStart) // 0..7 are NoPrediction, Ra, Rb, Rc, RaRbRc1, RaRbRc2, RaRbRc3, RaRb
	default:
		scan.predictor = jpegNoPrediction
		if spectralSelectionEnd == 0 {
			spectralSelectionEnd = 63
		}
		if spectralSelectionStart != 0 || spectralSelectionEnd != 63 {
			return nil, jpegFormatError("spectral selection is not allowed in non-progressive scan")
		}
		if scan.successiveHigh != 0 || scan.successiveLow != 0 {
			return nil, jpegFormatError("successive approximation is not allowed in non-progressive scan")
		}
	}
	scan.spectralStart = spectralSelectionStart
	scan.spectralEnd = spectralSelectionEnd + 1
	return scan, nil
}

// parseDQT is parser.rs:479-528 parse_dqt: the tables of the segment, in zigzag order.
func (r *jpegReader) parseDQT() (tables [4]*[64]uint16, err error) {
	length, err := r.readLength(jpegMarkerDQT)
	if err != nil {
		return tables, err
	}
	// Each DQT segment may contain multiple quantization tables.
	for length > 0 {
		b, err := r.readU8()
		if err != nil {
			return tables, err
		}
		precision, index := int(b>>4), int(b&0x0f)
		// 8-bit samples with a 16-bit table are allowed, as libjpeg does (parser.rs:488-497).
		if precision > 1 {
			return tables, jpegFormatError("invalid precision %d in DQT", precision)
		}
		if index > 3 {
			return tables, jpegFormatError("invalid destination identifier %d in DQT", index)
		}
		if length < 65+64*precision {
			return tables, jpegFormatError("invalid length in DQT")
		}
		table := new([64]uint16)
		for i := range table {
			if precision == 0 {
				v, err := r.readU8()
				if err != nil {
					return tables, err
				}
				table[i] = uint16(v)
			} else {
				v, err := r.readU16BE()
				if err != nil {
					return tables, err
				}
				table[i] = v
			}
		}
		for _, v := range table {
			if v == 0 {
				return tables, jpegFormatError("quantization table contains element with a zero value")
			}
		}
		tables[index] = table
		length -= 65 + 64*precision
	}
	return tables, nil
}

// parseDHT is parser.rs:530-584 parse_dht. isBaseline is `Option<bool>`: the frame's flag, or none before the frame.
func (r *jpegReader) parseDHT(haveFrame, isBaseline bool) (dc, ac [4]*jpegHuffmanTable, err error) {
	length, err := r.readLength(jpegMarkerDHT)
	if err != nil {
		return dc, ac, err
	}
	// Each DHT segment may contain multiple huffman tables.
	for length > 17 {
		b, err := r.readU8()
		if err != nil {
			return dc, ac, err
		}
		class, index := b>>4, int(b&0x0f)
		if class != 0 && class != 1 {
			return dc, ac, jpegFormatError("invalid class %d in DHT", class)
		}
		if haveFrame && isBaseline && index > 1 {
			return dc, ac, jpegFormatError("a maximum of two huffman tables per class are allowed in baseline")
		}
		if index > 3 {
			return dc, ac, jpegFormatError("invalid destination identifier %d in DHT", index)
		}
		countBytes, err := r.readExact(16)
		if err != nil {
			return dc, ac, err
		}
		var counts [16]uint8
		copy(counts[:], countBytes)
		size := 0
		for _, c := range counts {
			size += int(c)
		}
		switch {
		case size == 0:
			return dc, ac, jpegFormatError("encountered table with zero length in DHT")
		case size > 256:
			return dc, ac, jpegFormatError("encountered table with excessive length in DHT")
		case size > length-17:
			return dc, ac, jpegFormatError("invalid length in DHT")
		}
		values, err := r.readExact(size)
		if err != nil {
			return dc, ac, err
		}
		table, err := newJPEGHuffmanTable(&counts, values, class == 1)
		if err != nil {
			return dc, ac, err
		}
		if class == 0 {
			dc[index] = table
		} else {
			ac[index] = table
		}
		length -= 17 + size
	}
	if length != 0 {
		return dc, ac, jpegFormatError("invalid length in DHT")
	}
	return dc, ac, nil
}

// parseDRI is parser.rs:586-595 parse_dri.
func (r *jpegReader) parseDRI() (uint16, error) {
	length, err := r.readLength(jpegMarkerDRI)
	if err != nil {
		return 0, err
	}
	if length != 2 {
		return 0, jpegFormatError("DRI with invalid length")
	}
	return r.readU16BE()
}

// parseCOM is parser.rs:597-605 parse_com: the comment is read and dropped.
func (r *jpegReader) parseCOM() error {
	length, err := r.readLength(jpegMarkerCOM)
	if err != nil {
		return err
	}
	return r.skip(length)
}

// jpegAppData is the part of parser.rs:92-102 AppData the decoder acts on: JFIF, AVI1 and the Adobe colour transform (the Exif,
// XMP, ICC and PSIR payloads are read and dropped).
type jpegAppData struct {
	jfif, avi1 bool
	adobe      bool
	transform  jpegAdobeTransform
}

// parseAPP is parser.rs:607-704 parse_app.
func (r *jpegReader) parseAPP(marker byte) (data jpegAppData, err error) {
	length, err := r.readLength(marker)
	if err != nil {
		return data, err
	}
	bytesRead := 0

	switch marker - jpegMarkerAPP0 {
	case 0:
		if length >= 5 {
			buffer, err := r.readExact(5)
			if err != nil {
				return data, err
			}
			bytesRead = 5
			// http://www.w3.org/Graphics/JPEG/jfif3.pdf
			if string(buffer) == "JFIF\x00" {
				data.jfif = true
			} else if string(buffer) == "AVI1\x00" {
				data.avi1 = true
			}
		}
	case 1:
		// Exif and XMP: the whole segment is read (parser.rs:630-650).
		if _, err := r.readExact(length); err != nil {
			return data, err
		}
		bytesRead = length
	case 2:
		if length > 14 {
			buffer, err := r.readExact(14)
			if err != nil {
				return data, err
			}
			bytesRead = 14
			// ICC_PROFILE chunks read the rest of the segment.
			if string(buffer[:12]) == "ICC_PROFILE\x00" {
				if _, err := r.readExact(length - bytesRead); err != nil {
					return data, err
				}
				bytesRead = length
			}
		}
	case 13:
		if length >= 14 {
			buffer, err := r.readExact(14)
			if err != nil {
				return data, err
			}
			bytesRead = 14
			if string(buffer) == "Photoshop 3.0\x00" {
				if _, err := r.readExact(length - bytesRead); err != nil {
					return data, err
				}
				bytesRead = length
			}
		}
	case 14:
		if length >= 12 {
			buffer, err := r.readExact(12)
			if err != nil {
				return data, err
			}
			bytesRead = 12
			// http://www.sno.phy.queensu.ca/~phil/exiftool/TagNames/JPEG.html#Adobe
			if string(buffer[:6]) == "Adobe\x00" {
				switch buffer[11] {
				case 0:
					data.transform = jpegAdobeUnknown
				case 1:
					data.transform = jpegAdobeYCbCr
				case 2:
					data.transform = jpegAdobeYCCK
				default:
					return data, jpegFormatError("invalid color transform in adobe app segment")
				}
				data.adobe = true
			}
		}
	}

	if err := r.skip(length - bytesRead); err != nil {
		return data, err
	}
	return data, nil
}
