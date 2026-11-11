// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import "encoding/binary"

// Ports jpeg-decoder 0.3.1 src/decoder/lossless.rs (cited as lossless.rs).

// decodeScanLossless is lossless.rs:11-187 decode_scan_lossless: one component plane of u16 samples per component of the scan, and
// the marker that followed the scan.
func (d *jpegDecoder) decodeScanLossless(frame *jpegFrame, scan *jpegScan) (marker byte, hasMarker bool, results [][]uint16, err error) {
	ncomp := scan.count
	npixel := int(frame.imageSize.height) * int(frame.imageSize.width)
	results = make([][]uint16, ncomp)
	for i := range results {
		results[i] = make([]uint16, npixel)
	}

	// Verify that all required huffman tables has been set.
	for _, i := range scan.dcTableIndices[:ncomp] {
		if d.dcTables[i] == nil {
			return 0, false, nil, jpegFormatError("scan makes use of unset dc huffman table")
		}
	}

	var huffman jpegHuffmanDecoder
	reader := &d.r
	mcusLeftUntilRestart := d.restartInterval
	expectedRstNum := uint8(0)
	var ra, rb, rc [jpegMaxComponents]uint16

	width := int(frame.imageSize.width)
	height := int(frame.imageSize.height)

	differences := make([][]int32, ncomp)
	for i := range differences {
		differences[i] = make([]int32, 0, npixel)
	}
	for range height {
		for range width {
			if d.restartInterval > 0 {
				if mcusLeftUntilRestart == 0 {
					m, ok, err := huffman.takeMarker(reader)
					if err != nil {
						return 0, false, nil, err
					}
					switch {
					case ok && jpegIsRST(m):
						if n := m - jpegMarkerRST0; n != expectedRstNum {
							return 0, false, nil, jpegFormatError("found RST%d where RST%d was expected", n, expectedRstNum)
						}
						huffman.reset()
						expectedRstNum = (expectedRstNum + 1) % 8
						mcusLeftUntilRestart = d.restartInterval
					case ok:
						return 0, false, nil, jpegFormatError("found marker 0x%02X inside scan where RST%d was expected", m, expectedRstNum)
					default:
						return 0, false, nil, jpegFormatError("no marker found where RST%d was expected", expectedRstNum)
					}
				}
				mcusLeftUntilRestart--
			}

			for i := range ncomp {
				dcTable := d.dcTables[scan.dcTableIndices[i]]
				value, err := huffman.decode(reader, dcTable)
				if err != nil {
					return 0, false, nil, err
				}
				var diff int32
				switch {
				case value == 0:
				case value <= 15:
					v, err := huffman.receiveExtend(reader, value)
					if err != nil {
						return 0, false, nil, err
					}
					diff = int32(v)
				case value == 16:
					diff = 32768
				default:
					// Section F.1.2.1.1, Table F.1
					return 0, false, nil, jpegFormatError("invalid DC difference magnitude category")
				}
				differences[i] = append(differences[i], diff)
			}
		}
	}

	pointTransform := scan.pointTransform
	if scan.predictor == jpegPredRa {
		for i := range ncomp {
			// calculate the top left pixel; `1 << (precision - point_transform - 1) as i32` shifts an i32, in release mode the
			// shift count wraps modulo 32 (and the u8 subtraction modulo 256)
			diff := differences[i][0]
			prediction := int32(1) << (uint32(frame.precision-pointTransform-1) & 31)
			result := uint16((prediction + diff) & 0xFFFF) // modulo 2^16
			result <<= pointTransform & 15
			results[i][0] = result

			// calculate leftmost column, using top pixel as predictor
			previous := result
			for y := 1; y < height; y++ {
				diff := differences[i][y*width]
				result := uint16((int32(previous) + diff) & 0xFFFF)
				result <<= pointTransform & 15
				results[i][y*width] = result
				previous = result
			}

			// calculate rows, using left pixel as predictor
			for y := range height {
				for x := 1; x < width; x++ {
					diff := differences[i][y*width+x]
					prediction := int32(results[i][y*width+x-1])
					result := uint16((prediction + diff) & 0xFFFF)
					result <<= pointTransform & 15
					results[i][y*width+x] = result
				}
			}
		}
	} else {
		// `restart` of predict is evaluated here, after the whole scan was read, so it holds the final counter (lossless.rs:169-170).
		restart := d.restartInterval > 0 && mcusLeftUntilRestart == d.restartInterval-1
		for y := range height {
			for x := range width {
				for i := range ncomp {
					diff := differences[i][y*width+x]
					// The checks and updates of the previous values stay as in lossless.rs:149-160.
					if x > 0 {
						ra[i] = results[i][y*width+x-1]
					}
					if y > 0 {
						rb[i] = results[i][(y-1)*width+x]
						if x > 0 {
							rc[i] = results[i][(y-1)*width+x-1]
						}
					}
					prediction := jpegPredict(int32(ra[i]), int32(rb[i]), int32(rc[i]), scan.predictor, pointTransform, frame.precision, x, y, restart)
					result := uint16((prediction + diff) & 0xFFFF)
					results[i][y*width+x] = result << (pointTransform & 15)
				}
			}
		}
	}

	marker, hasMarker, err = huffman.takeMarker(&d.r)
	if err != nil {
		return 0, false, nil, err
	}
	for hasMarker && jpegIsRST(marker) {
		m, err := d.readMarker()
		marker, hasMarker = m, err == nil
	}
	return marker, hasMarker, results, nil
}

// jpegPredict is lossless.rs:189-226 predict (H.1.2.1).
func jpegPredict(ra, rb, rc int32, predictor jpegPredictor, pointTransform, inputPrecision uint8, ix, iy int, restart bool) int32 {
	switch {
	case (ix == 0 && iy == 0) || restart:
		// start of first line or restart
		if inputPrecision > 1+pointTransform {
			return 1 << (inputPrecision - pointTransform - 1)
		}
		return 0
	case iy == 0:
		// rest of first line
		return ra
	case ix == 0:
		// start of other line
		return rb
	}
	// use predictor Table H.1
	switch predictor {
	case jpegNoPrediction:
		return 0
	case jpegPredRa:
		return ra
	case jpegPredRb:
		return rb
	case jpegPredRc:
		return rc
	case jpegPredRaRbRc1:
		return ra + rb - rc
	case jpegPredRaRbRc2:
		return ra + ((rb - rc) >> 1)
	case jpegPredRaRbRc3:
		return rb + ((ra - rc) >> 1)
	default: // jpegPredRaRb
		return (ra + rb) / 2
	}
}

// computeImageLossless is lossless.rs:228-260 compute_image_lossless and convert_to_u8: the planes of the scan become interleaved
// bytes, one byte per sample at a precision of 8, two (little endian: wasm32's native order) otherwise.
func computeImageLossless(frame *jpegFrame, data [][]uint16) ([]byte, error) {
	if len(data) == 0 {
		return nil, jpegFormatError("not all components have data")
	}
	for _, plane := range data {
		if len(plane) == 0 {
			return nil, jpegFormatError("not all components have data")
		}
	}
	outputSize := frame.outputSize
	ncomp := len(frame.components)

	var samples []uint16
	if ncomp == 1 {
		samples = data[0]
	} else {
		samples = make([]uint16, ncomp*int(outputSize.width)*int(outputSize.height))
		for x := range len(samples) / ncomp {
			for i := range ncomp {
				samples[x*ncomp+i] = data[i][x]
			}
		}
	}

	if frame.precision == 8 {
		out := make([]byte, len(samples))
		for i, s := range samples {
			out[i] = byte(s)
		}
		return out, nil
	}
	out := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[2*i:], s)
	}
	return out, nil
}
