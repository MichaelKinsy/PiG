// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"errors"
	"fmt"
	"image"
	"math"
)

// jpegPixelFormat is decoder.rs:38-49 PixelFormat.
type jpegPixelFormat uint8

const (
	jpegL8 jpegPixelFormat = iota
	jpegL16
	jpegRGB24
	jpegCMYK32
)

// jpegMaxAlloc is image 0.24.9 `Limits::default().max_alloc` (io/mod.rs:63-71): `load_inner` reserves `decoder.total_bytes()` of it
// (io/free_functions.rs:100-111) before decoding, and fails when the decoded image is larger.
const jpegMaxAlloc = 512 * 1024 * 1024

// jpegDecoder is decoder.rs:100-130 Decoder over an in-memory input (the image crate hands it a `Cursor` over the byte slice).
type jpegDecoder struct {
	r jpegReader

	frame              *jpegFrame
	dcTables, acTables [4]*jpegHuffmanTable
	quantizationTables [4]*[64]uint16

	restartInterval uint16

	hasAdobe       bool
	adobeTransform jpegAdobeTransform

	isJFIF, isMJPEG bool

	// Used for progressive JPEGs.
	coefficients [][]int16
	// Bitmask of which coefficients has been completely decoded.
	coefficientsFinished [jpegMaxComponents]uint64
}

// DecodeJPEG returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a JPEG: the pixels
// of jpeg-decoder 0.3.1 as image 0.24.9 hands them to `DynamicImage::to_rgba8` (grayscale as gray with alpha 255, three components
// as RGB, four as CMYK or YCCK converted to RGB by the image crate's cmyk_to_rgb, 16-bit lossless grayscale scaled to 8 bits), and an
// error exactly where Photon throws: the formats jpeg-decoder rejects (arithmetic coding, hierarchical and differential frames, DNL,
// 12-bit samples, component counts other than 1, 3 and 4, subsampling ratios that are not integers), corrupt or truncated data, and
// an image whose decoded size exceeds the 512 MiB of image's default limits. A progressive JPEG that lacks scans is decoded from
// the coefficients it has, as jpeg-decoder does.
//
// The memory an input can ask for is the one the Rust decoder asks for: planes and coefficients of the frame header's size, which
// the 512 MiB limit on width * height * bytes per pixel bounds (about 1.5 GiB at most for the progressive coefficients, planes and
// output together); the 4 GiB address space of the wasm module, where Photon's allocation would fail first, is not emulated.
func DecodeJPEG(data []byte) (img *image.NRGBA, err error) {
	// A Go runtime panic (an index out of range) is where the Rust decoder panics, which Photon's `unwrap` turns into an exception.
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("photonimage: jpeg: decoder panic: %v", r)
		}
	}()
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		// image::guess_format (io/free_functions.rs:268, :304-312) finds a JPEG by the signature FF D8 FF.
		return nil, errors.New("photonimage: jpeg: not a JPEG (no FF D8 FF signature)")
	}
	d := &jpegDecoder{r: jpegReader{data: data}}
	return d.decode()
}

// jpegLosslessToNRGBA is `read_image` of image 0.24.9 (codecs/jpeg/decoder.rs:106-117) with `to_rgba8` for the bytes of a lossless
// frame: the other processes are converted to RGBA row by row (computeImageRGBA).
func jpegLosslessToNRGBA(frame *jpegFrame, pixels []byte, format jpegPixelFormat) (*image.NRGBA, error) {
	width, height := int(frame.outputSize.width), int(frame.outputSize.height)

	// `read_image` converts CMYK to RGB (cmyk_to_rgb) and asserts the buffer has `total_bytes()`, which holds a CMYK image as RGB24.
	if format == jpegCMYK32 {
		pixels = jpegCMYKToRGB(pixels)
		format = jpegRGB24
	}
	bytesPerPixel := 3
	switch format {
	case jpegL8:
		bytesPerPixel = 1
	case jpegL16:
		bytesPerPixel = 2
	}
	if len(pixels) != width*height*bytesPerPixel {
		return nil, errors.New("photonimage: jpeg: decoded buffer does not have the size of the image (`copy_from_slice` panics)")
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	out := img.Pix
	switch format {
	case jpegL8:
		for i, v := range pixels {
			out[4*i], out[4*i+1], out[4*i+2], out[4*i+3] = v, v, v, 255
		}
	case jpegL16:
		// image color.rs:408-420 FromPrimitive<u16> for u8, `(c + 128) / 257`; the samples are in native (little endian) order.
		for i := range len(pixels) / 2 {
			sample := uint32(pixels[2*i]) | uint32(pixels[2*i+1])<<8
			v := byte((sample + 128) / 257)
			out[4*i], out[4*i+1], out[4*i+2], out[4*i+3] = v, v, v, 255
		}
	default:
		for i := range len(pixels) / 3 {
			out[4*i], out[4*i+1], out[4*i+2], out[4*i+3] = pixels[3*i], pixels[3*i+1], pixels[3*i+2], 255
		}
	}
	return img, nil
}

// jpegCMYKToRGB is image 0.24.9 codecs/jpeg/decoder.rs:122-143 cmyk_to_rgb.
func jpegCMYKToRGB(input []byte) []byte {
	count := len(input) / 4
	output := make([]byte, 3*count)
	for i := range count {
		c := 255 - uint16(input[4*i])
		m := 255 - uint16(input[4*i+1])
		y := 255 - uint16(input[4*i+2])
		k := 255 - uint16(input[4*i+3])
		// CMY -> RGB
		output[3*i] = byte((k * c) / 255)
		output[3*i+1] = byte((k * m) / 255)
		output[3*i+2] = byte((k * y) / 255)
	}
	return output
}

// decode is JpegDecoder::new (image codecs/jpeg/decoder.rs:19-35: `read_info`), the limits of `load_inner` and `read_image`
// (:106-117) with `to_rgba8`, over decoder.rs:297-615 decode_internal (the two calls the image crate makes, `read_info` and
// `decode`, are one pass here: `read_info` stops after the frame header and `decode` goes on with the same reader).
func (d *jpegDecoder) decode() (*image.NRGBA, error) {
	if b, err := d.r.readU8(); err != nil {
		return nil, err
	} else if b != 0xFF {
		return nil, jpegFormatError("first two bytes are not an SOI marker")
	}
	if b, err := d.r.readU8(); err != nil {
		return nil, err
	} else if b != jpegMarkerSOI {
		return nil, jpegFormatError("first two bytes are not an SOI marker")
	}

	var (
		previousMarker byte = jpegMarkerSOI
		pendingMarker  byte
		hasPending     bool
		scansProcessed int
		planes         [][]byte
		planesU16      [][]uint16
		worker         jpegWorker
		format         jpegPixelFormat
	)

scans:
	for {
		var marker byte
		if hasPending {
			marker, hasPending = pendingMarker, false
		} else {
			m, err := d.readMarker()
			if err != nil {
				return nil, err
			}
			marker = m
		}

		switch {
		// Frame header
		case jpegIsSOF(marker):
			// Section 4.10: an image contains only one frame in the cases of sequential and progressive coding processes.
			if d.frame != nil {
				return nil, jpegUnsupported("Hierarchical")
			}
			frame, err := d.r.parseSOF(marker)
			if err != nil {
				return nil, err
			}
			componentCount := len(frame.components)
			if frame.isDifferential {
				return nil, jpegUnsupported("Hierarchical")
			}
			if frame.arithmetic {
				return nil, jpegUnsupported("ArithmeticEntropyCoding")
			}
			if frame.precision != 8 && frame.codingProcess != jpegLossless {
				return nil, jpegUnsupported(fmt.Sprintf("SamplePrecision(%d)", frame.precision))
			}
			if frame.precision < 2 || frame.precision > 16 {
				return nil, jpegUnsupported(fmt.Sprintf("SamplePrecision(%d)", frame.precision))
			}
			if componentCount != 1 && componentCount != 3 && componentCount != 4 {
				return nil, jpegUnsupported(fmt.Sprintf("ComponentCount(%d)", componentCount))
			}
			// Make sure we support the subsampling ratios used.
			if _, err := newJPEGUpsampler(frame.components, frame.imageSize.width, frame.imageSize.height); err != nil {
				return nil, err
			}

			// Decoder::info (decoder.rs:171-194) and the limits of image's load_inner: width * height * the bytes of the colour type
			// (a CMYK image counts as RGB24, the wrapper converts it) must fit max_alloc.
			bytesPerPixel := 3
			switch {
			case componentCount == 1 && frame.precision <= 8:
				format, bytesPerPixel = jpegL8, 1
			case componentCount == 1:
				format, bytesPerPixel = jpegL16, 2
			case componentCount == 3:
				format = jpegRGB24
			default:
				format = jpegCMYK32
			}
			if uint64(frame.outputSize.width)*uint64(frame.outputSize.height)*uint64(bytesPerPixel) > jpegMaxAlloc {
				return nil, errors.New("photonimage: jpeg: image exceeds the 512 MiB allocation limit of image 0.24.9")
			}

			d.frame = frame
			planes = make([][]byte, componentCount)
			planesU16 = make([][]uint16, componentCount)

		// Scan header
		case marker == jpegMarkerSOS:
			if d.frame == nil {
				return nil, jpegFormatError("scan encountered before frame")
			}
			frame := d.frame
			scan, err := d.r.parseSOS(frame)
			if err != nil {
				return nil, err
			}

			if frame.codingProcess == jpegDCTProgressive && len(d.coefficients) == 0 {
				d.coefficients = make([][]int16, len(frame.components))
				for i := range frame.components {
					c := &frame.components[i]
					d.coefficients[i] = make([]int16, int(c.blockSize.width)*int(c.blockSize.height)*64)
				}
			}

			if frame.codingProcess == jpegLossless {
				m, ok, data, err := d.decodeScanLossless(frame, scan)
				if err != nil {
					return nil, err
				}
				for i, plane := range data {
					if len(plane) != 0 {
						planesU16[i] = plane
					}
				}
				pendingMarker, hasPending = m, ok
			} else {
				// A progressive scan can deliver coefficient 0 of every component while only some of them get their last
				// coefficients, so which components a scan completes is tracked per component (decoder.rs:426-454).
				var finished [jpegMaxComponents]bool
				if scan.successiveLow == 0 {
					for pos := range scan.count {
						i := scan.componentIndices[pos]
						if d.coefficientsFinished[i] == math.MaxUint64 {
							continue
						}
						for j := scan.spectralStart; j < scan.spectralEnd; j++ {
							d.coefficientsFinished[i] |= 1 << j
						}
						if d.coefficientsFinished[i] == math.MaxUint64 {
							finished[pos] = true
						}
					}
				}

				m, ok, data, hasData, err := d.decodeScan(frame, scan, &worker, &finished)
				if err != nil {
					return nil, err
				}
				if hasData {
					for i, plane := range data {
						if len(plane) != 0 && d.coefficientsFinished[i] == math.MaxUint64 {
							planes[i] = plane
						}
					}
				}
				pendingMarker, hasPending = m, ok
			}
			scansProcessed++

		// Quantization table-specification
		case marker == jpegMarkerDQT:
			tables, err := d.r.parseDQT()
			if err != nil {
				return nil, err
			}
			for i, table := range tables {
				if table == nil {
					continue
				}
				unzigzagged := new([64]uint16)
				for j := range 64 {
					unzigzagged[unzigzag[j]] = table[j]
				}
				d.quantizationTables[i] = unzigzagged
			}

		// Huffman table-specification
		case marker == jpegMarkerDHT:
			dc, ac, err := d.r.parseDHT(d.frame != nil, d.frame != nil && d.frame.isBaseline)
			if err != nil {
				return nil, err
			}
			for i := range dc {
				if dc[i] != nil {
					d.dcTables[i] = dc[i]
				}
				if ac[i] != nil {
					d.acTables[i] = ac[i]
				}
			}

		// Arithmetic conditioning table-specification
		case marker == jpegMarkerDAC:
			return nil, jpegUnsupported("ArithmeticEntropyCoding")

		// Restart interval definition
		case marker == jpegMarkerDRI:
			interval, err := d.r.parseDRI()
			if err != nil {
				return nil, err
			}
			d.restartInterval = interval

		// Comment
		case marker == jpegMarkerCOM:
			if err := d.r.parseCOM(); err != nil {
				return nil, err
			}

		// Application data
		case marker >= jpegMarkerAPP0 && marker <= jpegMarkerAPP15:
			data, err := d.r.parseAPP(marker)
			if err != nil {
				return nil, err
			}
			switch {
			case data.adobe:
				d.hasAdobe, d.adobeTransform = true, data.transform
			case data.jfif:
				// Some JPEGs in the wild do not put the JFIF header right after SOI, so it is allowed anywhere APP0 is.
				d.isJFIF = true
			case data.avi1:
				d.isMJPEG = true
			}

		// Restart
		case jpegIsRST(marker):
			// Some encoders emit a final RST marker after entropy-coded data, which decode_scan does not take care of.
			if previousMarker != jpegMarkerSOS {
				return nil, jpegFormatError("RST found outside of entropy-coded data")
			}

		// Define number of lines
		case marker == jpegMarkerDNL:
			// Section B.2.1: "If a DNL segment is present, it shall immediately follow the first scan."
			if previousMarker != jpegMarkerSOS || scansProcessed != 1 {
				return nil, jpegFormatError("DNL is only allowed immediately after the first scan")
			}
			return nil, jpegUnsupported("DNL")

		// Hierarchical mode markers
		case marker == jpegMarkerDHP || marker == jpegMarkerEXP:
			return nil, jpegUnsupported("Hierarchical")

		// End of image
		case marker == jpegMarkerEOI:
			break scans

		default:
			return nil, jpegFormatError("marker 0x%02X found where not allowed", marker)
		}
		previousMarker = marker
	}

	if d.frame == nil {
		return nil, jpegFormatError("end of image encountered before frame")
	}
	return d.decodePlanes(&worker, planes, planesU16, format)
}

// decodePlanes is decoder.rs:617-696 decode_planes and what follows it in the image crate: the RGBA pixels. (Its check of the
// decoded size against `decoding_buffer_size_limit`, which is usize::MAX, can only fail for images of more than 4 GiB on wasm32,
// and the 512 MiB limit of DecodeJPEG is much below.)
func (d *jpegDecoder) decodePlanes(worker *jpegWorker, planes [][]byte, planesU16 [][]uint16, format jpegPixelFormat) (*image.NRGBA, error) {
	frame := d.frame

	// If we're decoding a progressive jpeg and a component is unfinished, render what we've got.
	if frame.codingProcess == jpegDCTProgressive && len(d.coefficients) == len(frame.components) {
		for i := range frame.components {
			component := &frame.components[i]
			// Only dealing with unfinished components
			if d.coefficientsFinished[i] == math.MaxUint64 {
				continue
			}
			quantizationTable := d.quantizationTables[component.quantizationTableIndex]
			if quantizationTable == nil {
				continue
			}
			if err := worker.start(i, component, quantizationTable); err != nil {
				return nil, err
			}
			// Send the rows over to the worker and collect the result.
			coefficientsPerMCURow := int(component.blockSize.width) * int(component.vSampling) * 64
			for mcuY := range int(frame.mcuSize.height) {
				offset := mcuY * coefficientsPerMCURow
				worker.appendRow(i, d.coefficients[i][offset:offset+coefficientsPerMCURow])
			}
			planes[i] = worker.getResult(i)
		}
	}

	if frame.codingProcess == jpegLossless {
		pixels, err := computeImageLossless(frame, planesU16)
		if err != nil {
			return nil, err
		}
		return jpegLosslessToNRGBA(frame, pixels, format)
	}
	return computeImageRGBA(frame.components, planes, frame.outputSize, d.determineColorTransform())
}

// determineColorTransform is decoder.rs:698-764 determine_color_transform (`set_color_transform` is never called by the image
// crate), after the logic of https://entropymine.wordpress.com/2018/10/22/how-is-a-jpeg-images-color-type-determined/.
func (d *jpegDecoder) determineColorTransform() jpegColorTransform {
	frame := d.frame
	count := len(frame.components)
	if count == 1 {
		return jpegTransformGrayscale
	}

	if count == 3 {
		switch [3]uint8{frame.components[0].identifier, frame.components[1].identifier, frame.components[2].identifier} {
		case [3]uint8{1, 2, 3}:
			return jpegTransformYCbCr
		case [3]uint8{1, 34, 35}:
			return jpegTransformJcsBgYcc
		case [3]uint8{82, 71, 66}:
			return jpegTransformRGB
		case [3]uint8{114, 103, 98}:
			return jpegTransformJcsBgRgb
		}
		if d.isJFIF {
			return jpegTransformYCbCr
		}
	}

	if d.hasAdobe {
		switch d.adobeTransform {
		case jpegAdobeUnknown:
			switch count {
			case 3:
				return jpegTransformRGB
			case 4:
				return jpegTransformCMYK
			}
		case jpegAdobeYCbCr:
			return jpegTransformYCbCr
		case jpegAdobeYCCK:
			return jpegTransformYCCK
		}
	} else if count == 4 {
		return jpegTransformCMYK
	}

	// The frame has 3 or 4 components here (`Unknown` is for any other count, which decode_internal has refused).
	if count == 4 {
		return jpegTransformYCCK
	}
	return jpegTransformYCbCr
}

// readMarker is decoder.rs:766-792 read_marker.
func (d *jpegDecoder) readMarker() (byte, error) {
	for {
		// The JPEG spec does not allow extraneous data between marker segments, libjpeg does, and so does this decoder.
		for {
			b, err := d.r.readU8()
			if err != nil {
				return 0, err
			}
			if b == 0xFF {
				break
			}
		}

		// Section B.1.1.2: any marker may optionally be preceded by any number of fill bytes.
		b, err := d.r.readU8()
		if err != nil {
			return 0, err
		}
		for b == 0xFF {
			b, err = d.r.readU8()
			if err != nil {
				return 0, err
			}
		}
		if b != 0x00 && b != 0xFF {
			return b, nil
		}
	}
}

// decodeScan is decoder.rs:794-1084 decode_scan: it decodes one scan into the coefficients (progressive) or straight into MCU
// rows for the worker, and returns the marker that ended the scan and, when the scan completed a component, the planes of the
// completed components (indexed by frame component).
func (d *jpegDecoder) decodeScan(frame *jpegFrame, scan *jpegScan, worker *jpegWorker, finished *[jpegMaxComponents]bool) (marker byte, hasMarker bool, data [][]byte, hasData bool, err error) {
	ncomp := scan.count
	var components [jpegMaxComponents]*jpegComponent
	for i := range ncomp {
		components[i] = &frame.components[scan.componentIndices[i]]
	}

	// Verify that all required quantization tables has been set.
	for i := range ncomp {
		if d.quantizationTables[components[i].quantizationTableIndex] == nil {
			return 0, false, nil, false, jpegFormatError("use of unset quantization table")
		}
	}

	if d.isMJPEG {
		fillDefaultMJPEGTables(scan, &d.dcTables, &d.acTables)
	}

	// Verify that all required huffman tables has been set.
	if scan.spectralStart == 0 {
		for _, i := range scan.dcTableIndices[:ncomp] {
			if d.dcTables[i] == nil {
				return 0, false, nil, false, jpegFormatError("scan makes use of unset dc huffman table")
			}
		}
	}
	if scan.spectralEnd > 1 {
		for _, i := range scan.acTableIndices[:ncomp] {
			if d.acTables[i] == nil {
				return 0, false, nil, false, jpegFormatError("scan makes use of unset ac huffman table")
			}
		}
	}

	// Prepare the worker for the work to come.
	for i := range ncomp {
		if finished[i] {
			if err := worker.start(i, components[i], d.quantizationTables[components[i].quantizationTableIndex]); err != nil {
				return 0, false, nil, false, err
			}
		}
	}

	isProgressive := frame.codingProcess == jpegDCTProgressive
	isInterleaved := ncomp > 1
	var dummyBlock [64]int16
	var huffman jpegHuffmanDecoder
	var dcPredictors [jpegMaxComponents]int16
	mcusLeftUntilRestart := d.restartInterval
	expectedRstNum := uint8(0)
	eobRun := uint16(0)
	var mcuRowCoefficients [jpegMaxComponents][]int16

	if !isProgressive {
		for i := range ncomp {
			if finished[i] {
				mcuRowCoefficients[i] = make([]int16, int(components[i].blockSize.width)*int(components[i].vSampling)*64)
			}
		}
	}

	// 4.8.2: when reading from the stream, if the data is non-interleaved then an MCU consists of exactly one block.
	var mcuHorizontalSamples, mcuVerticalSamples [jpegMaxComponents]uint16
	for i := range ncomp {
		if isInterleaved {
			mcuHorizontalSamples[i] = uint16(components[i].hSampling)
			mcuVerticalSamples[i] = uint16(components[i].vSampling)
		} else {
			mcuHorizontalSamples[i], mcuVerticalSamples[i] = 1, 1
		}
	}

	// This also affects how many MCU values we read from stream: for a non-interleaved stream the MCUs are exactly the blocks.
	var maxMCUX, maxMCUY uint16
	if isInterleaved {
		maxMCUX, maxMCUY = frame.mcuSize.width, frame.mcuSize.height
	} else {
		maxMCUX, maxMCUY = components[0].blockSize.width, components[0].blockSize.height
	}

	// The Rust code does this arithmetic in u16, where `mcu_y * 8` wraps for the last block rows of a 65535-line image.
	for mcuY := range maxMCUY {
		if mcuY*8 >= frame.imageSize.height {
			break
		}

		for mcuX := range maxMCUX {
			if mcuX*8 >= frame.imageSize.width {
				break
			}

			if d.restartInterval > 0 {
				if mcusLeftUntilRestart == 0 {
					m, ok, err := huffman.takeMarker(&d.r)
					if err != nil {
						return 0, false, nil, false, err
					}
					switch {
					case ok && jpegIsRST(m):
						if n := m - jpegMarkerRST0; n != expectedRstNum {
							return 0, false, nil, false, jpegFormatError("found RST%d where RST%d was expected", n, expectedRstNum)
						}
						huffman.reset()
						// Section F.2.1.3.1
						dcPredictors = [jpegMaxComponents]int16{}
						// Section G.1.2.2
						eobRun = 0
						expectedRstNum = (expectedRstNum + 1) % 8
						mcusLeftUntilRestart = d.restartInterval
					case ok:
						return 0, false, nil, false, jpegFormatError("found marker 0x%02X inside scan where RST%d was expected", m, expectedRstNum)
					default:
						return 0, false, nil, false, jpegFormatError("no marker found where RST%d was expected", expectedRstNum)
					}
				}
				mcusLeftUntilRestart--
			}

			for i := range ncomp {
				component := components[i]
				for vPos := range mcuVerticalSamples[i] {
					for hPos := range mcuHorizontalSamples[i] {
						var block *[64]int16
						switch {
						case isProgressive:
							blockY := int(mcuY*mcuVerticalSamples[i] + vPos)
							blockX := int(mcuX*mcuHorizontalSamples[i] + hPos)
							offset := (blockY*int(component.blockSize.width) + blockX) * 64
							block = (*[64]int16)(d.coefficients[scan.componentIndices[i]][offset : offset+64])
						case finished[i]:
							// The worker operates in batches as if we were always interleaved, so a non-interleaved stream
							// needs the row of the MCU batch it is in.
							var mcuBatchCurrentRow uint16
							if !isInterleaved {
								mcuBatchCurrentRow = mcuY % uint16(component.vSampling)
							}
							blockY := int(mcuBatchCurrentRow*mcuVerticalSamples[i] + vPos)
							blockX := int(mcuX*mcuHorizontalSamples[i] + hPos)
							offset := (blockY*int(component.blockSize.width) + blockX) * 64
							block = (*[64]int16)(mcuRowCoefficients[i][offset : offset+64])
						default:
							block = &dummyBlock
						}

						if scan.successiveHigh == 0 {
							err = d.decodeBlock(block, &huffman, d.dcTables[scan.dcTableIndices[i]], d.acTables[scan.acTableIndices[i]],
								scan.spectralStart, scan.spectralEnd, scan.successiveLow, &eobRun, &dcPredictors[i])
						} else {
							err = d.decodeBlockSuccessiveApproximation(block, &huffman, d.acTables[scan.acTableIndices[i]],
								scan.spectralStart, scan.spectralEnd, scan.successiveLow, &eobRun)
						}
						if err != nil {
							return 0, false, nil, false, err
						}
					}
				}
			}
		}

		// Send the coefficients from this MCU row to the worker for dequantization and idct.
		for i := range ncomp {
			if !finished[i] {
				continue
			}
			component := components[i]
			// In the event of non-interleaved streams, if we're still building the buffer out, keep going; don't send it yet.
			// We also need to ensure we don't skip over the last row(s) of the image.
			if !isInterleaved && (mcuY+1)*8 < frame.imageSize.height && (mcuY+1)%uint16(component.vSampling) > 0 {
				continue
			}

			coefficientsPerMCURow := int(component.blockSize.width) * int(component.vSampling) * 64
			if isProgressive {
				// Because non-interleaved streams will have multiple MCU rows concatenated together, the row for calculating the
				// offset is different.
				workerMCUY := mcuY
				if !isInterleaved {
					workerMCUY = mcuY / uint16(component.vSampling)
				}
				offset := int(workerMCUY) * coefficientsPerMCURow
				worker.appendRow(i, d.coefficients[scan.componentIndices[i]][offset:offset+coefficientsPerMCURow])
			} else {
				// `mem::replace(.., vec![0i16; n])`: the row goes to the worker and the next one starts from zeros.
				worker.appendRow(i, mcuRowCoefficients[i])
				clear(mcuRowCoefficients[i])
			}
		}
	}

	marker, hasMarker, err = huffman.takeMarker(&d.r)
	if err != nil {
		return 0, false, nil, false, err
	}
	for hasMarker && jpegIsRST(marker) {
		m, err := d.readMarker()
		marker, hasMarker = m, err == nil
	}

	// Retrieve all the data from the worker.
	for i := range ncomp {
		if finished[i] {
			if data == nil {
				data = make([][]byte, len(frame.components))
			}
			data[scan.componentIndices[i]] = worker.getResult(i)
			hasData = true
		}
	}
	return marker, hasMarker, data, hasData, nil
}

// decodeBlock is decoder.rs:1086-1172 decode_block: a block of a first scan (Section F.2.2.1 and G.1.2).
func (d *jpegDecoder) decodeBlock(coefficients *[64]int16, huffman *jpegHuffmanDecoder, dcTable, acTable *jpegHuffmanTable, specStart, specEnd, successiveLow uint8, eobRun *uint16, dcPredictor *int16) error {
	reader := &d.r
	if specStart == 0 {
		// Section F.2.2.1, Figure F.12
		value, err := huffman.decode(reader, dcTable)
		if err != nil {
			return err
		}
		var diff int16
		switch {
		case value == 0:
		case value <= 11:
			diff, err = huffman.receiveExtend(reader, value)
			if err != nil {
				return err
			}
		default:
			// Section F.1.2.1.1, Table F.1
			return jpegFormatError("invalid DC difference magnitude category")
		}

		// Malicious JPEG files can cause this add to overflow, so it wraps.
		*dcPredictor += diff
		coefficients[0] = *dcPredictor << successiveLow
	}

	index := max(specStart, 1)
	if index < specEnd && *eobRun > 0 {
		*eobRun--
		return nil
	}

	// Section F.1.2.2.1
	for index < specEnd {
		value, run, ok, err := huffman.decodeFastAC(reader, acTable)
		if err != nil {
			return err
		}
		if ok {
			index += run
			if index >= specEnd {
				break
			}
			coefficients[unzigzag[index]] = value << successiveLow
			index++
			continue
		}

		b, err := huffman.decode(reader, acTable)
		if err != nil {
			return err
		}
		r := b >> 4
		s := b & 0x0f

		if s == 0 {
			if r == 15 {
				index += 16 // Run length of 16 zero coefficients.
				continue
			}
			*eobRun = uint16(1)<<r - 1
			if r > 0 {
				bits, err := huffman.getBits(reader, r)
				if err != nil {
					return err
				}
				*eobRun += bits
			}
			break
		}

		index += r
		if index >= specEnd {
			break
		}
		v, err := huffman.receiveExtend(reader, s)
		if err != nil {
			return err
		}
		coefficients[unzigzag[index]] = v << successiveLow
		index++
	}
	return nil
}

// decodeBlockSuccessiveApproximation is decoder.rs:1174-1258 decode_block_successive_approximation: a block of a refinement scan
// (Section G.1.2).
func (d *jpegDecoder) decodeBlockSuccessiveApproximation(coefficients *[64]int16, huffman *jpegHuffmanDecoder, acTable *jpegHuffmanTable, specStart, specEnd, successiveLow uint8, eobRun *uint16) error {
	reader := &d.r
	bit := int16(1) << successiveLow

	if specStart == 0 {
		// Section G.1.2.1
		b, err := huffman.getBits(reader, 1)
		if err != nil {
			return err
		}
		if b == 1 {
			coefficients[0] |= bit
		}
		return nil
	}

	// Section G.1.2.3
	if *eobRun > 0 {
		*eobRun--
		_, err := jpegRefineNonZeroes(reader, coefficients, huffman, specStart, specEnd, 64, bit)
		return err
	}

	index := specStart
	for index < specEnd {
		b, err := huffman.decode(reader, acTable)
		if err != nil {
			return err
		}
		r := b >> 4
		s := b & 0x0f

		zeroRunLength := r
		var value int16

		switch s {
		case 0:
			if r != 15 {
				// With r = 15 there is nothing to do here: zero_run_length is 15 and then value (which is zero) gets written,
				// resulting in 16 zero coefficients.
				*eobRun = uint16(1)<<r - 1
				if r > 0 {
					bits, err := huffman.getBits(reader, r)
					if err != nil {
						return err
					}
					*eobRun += bits
				}
				// Force end of block.
				zeroRunLength = 64
			}
		case 1:
			b, err := huffman.getBits(reader, 1)
			if err != nil {
				return err
			}
			if b == 1 {
				value = bit
			} else {
				value = -bit
			}
		default:
			return jpegFormatError("unexpected huffman code")
		}

		index, err = jpegRefineNonZeroes(reader, coefficients, huffman, index, specEnd, zeroRunLength, bit)
		if err != nil {
			return err
		}
		if value != 0 {
			coefficients[unzigzag[index]] = value
		}
		index++
	}
	return nil
}

// jpegRefineNonZeroes is decoder.rs:1260-1298 refine_non_zeroes.
func jpegRefineNonZeroes(reader *jpegReader, coefficients *[64]int16, huffman *jpegHuffmanDecoder, start, end, zrl uint8, bit int16) (uint8, error) {
	last := end - 1
	zeroRunLength := zrl

	for i := start; i < end; i++ {
		coefficient := &coefficients[unzigzag[i]]

		if *coefficient == 0 {
			if zeroRunLength == 0 {
				return i, nil
			}
			zeroRunLength--
			continue
		}
		b, err := huffman.getBits(reader, 1)
		if err != nil {
			return 0, err
		}
		if b == 1 && *coefficient&bit == 0 {
			if *coefficient > 0 {
				if int32(*coefficient)+int32(bit) > math.MaxInt16 {
					return 0, jpegFormatError("Coefficient overflow")
				}
				*coefficient += bit
			} else {
				if int32(*coefficient)-int32(bit) < math.MinInt16 {
					return 0, jpegFormatError("Coefficient overflow")
				}
				*coefficient -= bit
			}
		}
	}
	return last, nil
}
