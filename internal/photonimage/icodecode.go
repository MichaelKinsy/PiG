// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
)

// icoSignature is image 0.24.9's guess_format signature for ICO (io/free_functions.rs:276): a zero reserved word and the type 1 (icon).
var icoSignature = []byte{0, 0, 1, 0}

// isICO is image::guess_format's test for an ICO file.
func isICO(data []byte) bool {
	return bytes.HasPrefix(data, icoSignature)
}

var (
	errICO      = errors.New("photonimage: ico: invalid or truncated file")
	errICOPNG   = errors.New("photonimage: ico: invalid embedded PNG")
	errICOEntry = errors.New("photonimage: ico: image does not match its directory entry")
)

// icoCursor is the `Cursor` over the byte slice that `IcoDecoder::new(Cursor)` reads and seeks. A position past the end is allowed, and
// reading there fails.
type icoCursor struct {
	data []byte
	pos  uint64
}

func (c *icoCursor) take(n uint64) ([]byte, bool) {
	size := uint64(len(c.data))
	if c.pos > size || size-c.pos < n {
		c.pos = size
		return nil, false
	}
	b := c.data[c.pos : c.pos+n]
	c.pos += n
	return b, true
}

func (c *icoCursor) u8() (byte, bool) {
	b, ok := c.take(1)
	if !ok {
		return 0, false
	}
	return b[0], true
}

func (c *icoCursor) u16() (uint16, bool) {
	b, ok := c.take(2)
	if !ok {
		return 0, false
	}
	return binary.LittleEndian.Uint16(b), true
}

func (c *icoCursor) u32() (uint32, bool) {
	b, ok := c.take(4)
	if !ok {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b), true
}

// icoDirEntry is decoder.rs:121-143 `DirEntry`.
type icoDirEntry struct {
	width, height byte
	bitsPerPixel  uint16
	imageLength   uint32
	imageOffset   uint32
}

func (e icoDirEntry) realWidth() uint32 {
	if e.width == 0 {
		return 256
	}
	return uint32(e.width)
}

func (e icoDirEntry) realHeight() uint32 {
	if e.height == 0 {
		return 256
	}
	return uint32(e.height)
}

// matchesDimensions is decoder.rs:232-235: the entry's size against the image's, compared up to 256.
func (e icoDirEntry) matchesDimensions(width, height uint32) bool {
	return e.realWidth() == min(width, 256) && e.realHeight() == min(height, 256)
}

// DecodeICO returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for an ICO file: image
// 0.24.9's IcoDecoder (codecs/ico/decoder.rs) over the entry with the highest (bits per pixel, area) (the last of equals), as `to_rgba8`
// converts it. The type word of the directory is not read, only the entries up to the count.
//
// An entry that starts with the PNG signature is a PNG, decoded as the png crate does (icopng.go), which must be 8-bit RGBA after its EXPAND
// transformation (RGBA, or RGB or palette with a tRNS chunk) and the size the entry names (compared up to 256); anything else is a bitmap (BmpDecoder in ICO
// format, icoBMP), with an AND mask that makes pixels transparent when the entry's length leaves room for one. A bitmap of more than 2^29
// bytes of RGBA (width * height / 2 * 4) or an empty image whose RGBA buffer length overflows wasm32's `usize` is an error, as is every
// other file image rejects.
func DecodeICO(data []byte) (*image.NRGBA, error) {
	if !isICO(data) {
		return nil, errors.New("photonimage: ico: not an ICO (no 00 00 01 00 signature)")
	}
	r := &icoCursor{data: data}
	// read_entries (decoder.rs:159-193).
	if _, ok := r.u16(); !ok { // reserved
		return nil, errICO
	}
	if _, ok := r.u16(); !ok { // type
		return nil, errICO
	}
	count, ok := r.u16()
	if !ok {
		return nil, errICO
	}
	entries := make([]icoDirEntry, 0, min(int(count), 4096))
	for range count {
		e, ok := icoReadEntry(r)
		if !ok {
			return nil, errICO
		}
		entries = append(entries, e)
	}
	// best_entry (decoder.rs:196-215): the last entry is the first candidate; the others replace it when strictly better.
	if len(entries) == 0 {
		return nil, errors.New("photonimage: ico: directory contains no image")
	}
	best := entries[len(entries)-1]
	bestDepth, bestArea := best.bitsPerPixel, best.realWidth()*best.realHeight()
	for _, e := range entries[:len(entries)-1] {
		depth, area := e.bitsPerPixel, e.realWidth()*e.realHeight()
		if depth > bestDepth || depth == bestDepth && area > bestArea {
			best, bestDepth, bestArea = e, depth, area
		}
	}

	// DirEntry::decoder: sniff the first 8 bytes at the image offset.
	r.pos = uint64(best.imageOffset)
	signature, ok := r.take(8)
	if !ok {
		return nil, errICO
	}
	if bytes.Equal(signature, []byte("\x89PNG\r\n\x1a\n")) {
		return icoDecodePNG(data[best.imageOffset:], best)
	}
	r.pos = uint64(best.imageOffset)
	return icoDecodeBMP(r, best)
}

// icoReadEntry is decoder.rs:166-193 `read_entry`.
func icoReadEntry(r *icoCursor) (icoDirEntry, bool) {
	b, ok := r.take(4)
	if !ok {
		return icoDirEntry{}, false
	}
	e := icoDirEntry{width: b[0], height: b[1]}
	planes, ok := r.u16()
	if !ok || planes > 256 { // the number of colour planes, or the hotspot of a cursor
		return icoDirEntry{}, false
	}
	if e.bitsPerPixel, ok = r.u16(); !ok || e.bitsPerPixel > 256 {
		return icoDirEntry{}, false
	}
	if e.imageLength, ok = r.u32(); !ok {
		return icoDirEntry{}, false
	}
	if e.imageOffset, ok = r.u32(); !ok {
		return icoDirEntry{}, false
	}
	return e, true
}

// icoDecodePNG is the PNG arm of IcoDecoder (decoder.rs:307-332) over `PngDecoder::new` (codecs/png.rs:119-199): the png crate's reader
// (icopng.go), whose output colour type under EXPAND has to be Rgba8.
func icoDecodePNG(data []byte, entry icoDirEntry) (*image.NRGBA, error) {
	p, err := icoPNGReadInfo(data)
	if err != nil {
		return nil, err
	}
	info := &p.info
	if entry.imageLength < 8 || !entry.matchesDimensions(info.width, info.height) {
		return nil, errICOEntry
	}
	if !info.outputRGBA() {
		return nil, errors.New("photonimage: ico: the PNG is not in RGBA format")
	}
	// load_inner reserves the decoder's total_bytes before it decodes.
	if err := reserveTotalBytes(info.width, info.height, 4); err != nil {
		return nil, err
	}
	if err := checkRGBAConversion(info.width, info.height, 4); err != nil {
		return nil, err
	}
	pix, err := icoPNGDecode(data, p)
	if err != nil {
		return nil, err
	}
	out := newImageNRGBA(0, 0)
	out.Pix, out.Stride, out.Rect = pix, int(info.width)*4, image.Rect(0, 0, int(info.width), int(info.height))
	return out, nil
}
