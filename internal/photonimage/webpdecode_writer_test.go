// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"encoding/binary"
	"math/bits"
	"slices"
	"sort"
)

// The writers of the WebP corpus. Photon is the oracle of the decoder, so the files do not have to look like pictures, they have to be
// valid streams that take every path of the decoder: a lossy frame is a header written with a boolean encoder followed by random bytes
// (the arithmetic decoder turns any bytes into symbols distributed as the probabilities say), a lossless image is written symbol by
// symbol with prefix codes built for the symbols it uses. Everything is deterministic: integers only, one xorshift32 generator.

// webpRNG is xorshift32.
type webpRNG uint32

func (r *webpRNG) next() uint32 {
	s := uint32(*r)
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*r = webpRNG(s)
	return s
}

// intn is a number in [0, n).
func (r *webpRNG) intn(n int) int {
	return int(r.next()>>4) % n
}

// chance is true percent times in a hundred.
func (r *webpRNG) chance(percent int) bool {
	return r.intn(100) < percent
}

func (r *webpRNG) bytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.next() >> 11)
	}
	return b
}

func newWebPRNG(seed uint32) *webpRNG {
	r := webpRNG(seed*2654435761 + 1)
	for range 4 {
		r.next()
	}
	return &r
}

// webpBoolWriter is the boolean entropy encoder of RFC 6386 section 7.3.
type webpBoolWriter struct {
	out      []byte
	rng      uint32
	bottom   uint32
	bitCount int
}

func newWebPBoolWriter() *webpBoolWriter {
	return &webpBoolWriter{rng: 255, bitCount: 24}
}

func (w *webpBoolWriter) addOneToOutput() {
	for i := len(w.out) - 1; i >= 0; i-- {
		if w.out[i] != 255 {
			w.out[i]++
			return
		}
		w.out[i] = 0
	}
}

func (w *webpBoolWriter) writeBool(prob uint8, value bool) {
	split := 1 + (((w.rng - 1) * uint32(prob)) >> 8)
	if value {
		w.bottom += split
		w.rng -= split
	} else {
		w.rng = split
	}
	for w.rng < 128 {
		w.rng <<= 1
		if w.bottom&(1<<31) != 0 {
			w.addOneToOutput()
		}
		w.bottom <<= 1
		w.bitCount--
		if w.bitCount == 0 {
			w.out = append(w.out, byte(w.bottom>>24))
			w.bottom &= 1<<24 - 1
			w.bitCount = 8
		}
	}
}

func (w *webpBoolWriter) literal(value, n int) {
	for i := n - 1; i >= 0; i-- {
		w.writeBool(128, value>>i&1 != 0)
	}
}

func (w *webpBoolWriter) flag(b bool) {
	w.writeBool(128, b)
}

// magnitudeAndSign is the inverse of read_magnitude_and_sign.
func (w *webpBoolWriter) magnitudeAndSign(value, n int) {
	w.literal(webpAbs(value), n)
	w.flag(value < 0)
}

func webpAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (w *webpBoolWriter) flush() []byte {
	c := w.bitCount
	v := w.bottom
	if v&(1<<(32-c)) != 0 {
		w.addOneToOutput()
	}
	v <<= c & 7
	c >>= 3
	for c--; c >= 0; c-- {
		v <<= 8
	}
	for range 4 {
		w.out = append(w.out, byte(v>>24))
		v <<= 8
	}
	return w.out
}

// webpLossyGen describes a lossy frame: the fields of the frame header, which the writer encodes, and the sizes of the random data that follows.
type webpLossyGen struct {
	width, height int
	scaleBits     int // the two bits above the dimensions, which the decoder ignores
	version       int
	show          bool
	colorSpace    int
	pixelType     int

	segments, updateMap, updateData, absolute bool
	quantUpdate, lfUpdate                     [4]bool
	quant, lf                                 [4]int
	treeProbUpdate                            [3]bool
	treeProbs                                 [3]int

	simple         bool
	level          int
	sharpness      int
	lfAdjust       bool
	lfDeltaUpdate  bool
	refUpdate      [4]bool
	modeUpdate     [4]bool
	refDelta       [4]int
	modeDelta      [4]int
	partitionsLog2 int
	yac            int
	qdeltaUpdate   [5]bool
	qdelta         [5]int
	refresh        int
	tokenUpdatePct int // percent of the token probabilities that are updated
	skipProb       int // -1: no skip flag

	headerExtra    int // random bytes after the header in the first partition
	partitionBytes int // random bytes in each partition
	seed           uint32
}

// build is the data of the VP8 chunk.
func (g *webpLossyGen) build() []byte {
	rng := newWebPRNG(g.seed)
	w := newWebPBoolWriter()

	w.literal(g.colorSpace, 1)
	w.literal(g.pixelType, 1)

	w.flag(g.segments)
	if g.segments {
		w.flag(g.updateMap)
		w.flag(g.updateData)
		if g.updateData {
			w.flag(g.absolute)
			for i := range 4 {
				w.flag(g.quantUpdate[i])
				if g.quantUpdate[i] {
					w.magnitudeAndSign(g.quant[i], 7)
				}
			}
			for i := range 4 {
				w.flag(g.lfUpdate[i])
				if g.lfUpdate[i] {
					w.magnitudeAndSign(g.lf[i], 6)
				}
			}
		}
		if g.updateMap {
			for i := range 3 {
				w.flag(g.treeProbUpdate[i])
				if g.treeProbUpdate[i] {
					w.literal(g.treeProbs[i], 8)
				}
			}
		}
	}

	w.flag(g.simple)
	w.literal(g.level, 6)
	w.literal(g.sharpness, 3)
	w.flag(g.lfAdjust)
	if g.lfAdjust {
		w.flag(g.lfDeltaUpdate)
		if g.lfDeltaUpdate {
			for i := range 4 {
				w.flag(g.refUpdate[i])
				if g.refUpdate[i] {
					w.magnitudeAndSign(g.refDelta[i], 6)
				}
			}
			for i := range 4 {
				w.flag(g.modeUpdate[i])
				if g.modeUpdate[i] {
					w.magnitudeAndSign(g.modeDelta[i], 6)
				}
			}
		}
	}

	w.literal(g.partitionsLog2, 2)

	w.literal(g.yac, 7)
	for i := range 5 {
		w.flag(g.qdeltaUpdate[i])
		if g.qdeltaUpdate[i] {
			w.magnitudeAndSign(g.qdelta[i], 4)
		}
	}

	w.literal(g.refresh, 1)

	for i := range vp8CoeffUpdateProbs {
		for j := range vp8CoeffUpdateProbs[i] {
			for k := range vp8CoeffUpdateProbs[i][j] {
				for _, prob := range vp8CoeffUpdateProbs[i][j][k] {
					update := rng.chance(g.tokenUpdatePct)
					w.writeBool(prob, update)
					if update {
						w.literal(rng.intn(256), 8)
					}
				}
			}
		}
	}

	if g.skipProb >= 0 {
		w.literal(1, 1)
		w.literal(g.skipProb, 8)
	} else {
		w.literal(0, 1)
	}

	first := append(w.flush(), rng.bytes(g.headerExtra)...)

	n := 1 << g.partitionsLog2
	out := make([]byte, 0, 10+len(first)+n*(g.partitionBytes+3))
	tag := uint32(g.version&7)<<1 | uint32(len(first))<<5
	if g.show {
		tag |= 1 << 4
	}
	out = append(out, byte(tag), byte(tag>>8), byte(tag>>16), 0x9d, 0x01, 0x2a)
	out = binary.LittleEndian.AppendUint16(out, uint16(g.width|g.scaleBits<<14))
	out = binary.LittleEndian.AppendUint16(out, uint16(g.height|g.scaleBits<<14))
	out = append(out, first...)

	partitions := make([][]byte, n)
	for i := range partitions {
		partitions[i] = rng.bytes(g.partitionBytes + rng.intn(5))
	}
	for _, p := range partitions[:n-1] {
		out = append(out, byte(len(p)), byte(len(p)>>8), byte(len(p)>>16))
	}
	for _, p := range partitions {
		out = append(out, p...)
	}
	return out
}

// webpChunkBytes is a RIFF chunk: the four character code, the length, the data and the padding byte.
func webpChunkBytes(fourcc string, payload []byte) []byte {
	out := append([]byte(fourcc), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(payload)))
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

// webpRIFF is a file of chunks.
func webpRIFF(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
	}
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}

// webpVP8XChunk is a VP8X chunk with the flags and canvas given.
func webpVP8XChunk(flags byte, width, height int) []byte {
	p := []byte{flags, 0, 0, 0, byte(width - 1), byte((width - 1) >> 8), byte((width - 1) >> 16), byte(height - 1), byte((height - 1) >> 8), byte((height - 1) >> 16)}
	return webpChunkBytes("VP8X", p)
}

// webpANMFChunk is an ANMF chunk of the frame at (x, y) (in units of two pixels), width by height, around the chunks of the frame.
func webpANMFChunk(x, y, width, height, duration int, flags byte, frame ...[]byte) []byte {
	p := []byte{byte(x), byte(x >> 8), byte(x >> 16), byte(y), byte(y >> 8), byte(y >> 16),
		byte(width - 1), byte((width - 1) >> 8), byte((width - 1) >> 16), byte(height - 1), byte((height - 1) >> 8), byte((height - 1) >> 16),
		byte(duration), byte(duration >> 8), byte(duration >> 16), flags}
	for _, f := range frame {
		p = append(p, f...)
	}
	return webpChunkBytes("ANMF", p)
}

// webpBitWriter writes bits least significant bit first.
type webpBitWriter struct {
	buf []byte
	acc uint64
	n   int
}

func (w *webpBitWriter) put(value uint32, nbits int) {
	w.acc |= uint64(value&(1<<nbits-1)) << w.n
	w.n += nbits
	for w.n >= 8 {
		w.buf = append(w.buf, byte(w.acc))
		w.acc >>= 8
		w.n -= 8
	}
}

func (w *webpBitWriter) bytes() []byte {
	out := slices.Clone(w.buf)
	if w.n > 0 {
		out = append(out, byte(w.acc))
	}
	return out
}

// webpPrefixCode is a prefix code the writer built for the symbols a stream uses.
type webpPrefixCode struct {
	lengths []int // normal code: the length of each symbol of the alphabet
	codes   []uint32
	single  bool // one symbol, no bits
	simple  []int
}

// webpHuffmanLengths gives the used symbols the lengths of a complete code with random weights, no longer than maxLen.
func webpHuffmanLengths(rng *webpRNG, used []int, maxLen int) map[int]int {
	n := len(used)
	weights := make([]int, n)
	for i := range weights {
		if rng.chance(30) {
			weights[i] = 1 << rng.intn(12)
		} else {
			weights[i] = 1 + rng.intn(64)
		}
	}
	for {
		lengths := webpHuffmanDepths(weights)
		if slices.Max(lengths) <= maxLen {
			out := make(map[int]int, n)
			for i, s := range used {
				out[s] = lengths[i]
			}
			return out
		}
		total := 0
		for _, w := range weights {
			total += w
		}
		for i := range weights {
			weights[i] += total / (4 * n)
		}
	}
}

// webpHuffmanDepths is the depth of each leaf of a Huffman tree of the weights (two or more).
func webpHuffmanDepths(weights []int) []int {
	type node struct {
		weight      int
		left, right int
		leaf        int
	}
	nodes := make([]node, 0, 2*len(weights))
	order := make([]int, len(weights))
	for i, w := range weights {
		nodes = append(nodes, node{weight: w, left: -1, right: -1, leaf: i})
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return nodes[order[a]].weight < nodes[order[b]].weight })
	queue := order
	for len(queue) > 1 {
		a, b := queue[0], queue[1]
		queue = queue[2:]
		nodes = append(nodes, node{weight: nodes[a].weight + nodes[b].weight, left: a, right: b, leaf: -1})
		id := len(nodes) - 1
		// insert keeping the queue sorted
		pos := sort.Search(len(queue), func(i int) bool { return nodes[queue[i]].weight > nodes[id].weight })
		queue = slices.Insert(slices.Clone(queue), pos, id)
	}
	depths := make([]int, len(weights))
	var walk func(id, depth int)
	walk = func(id, depth int) {
		if nodes[id].leaf >= 0 {
			depths[nodes[id].leaf] = depth
			return
		}
		walk(nodes[id].left, depth+1)
		walk(nodes[id].right, depth+1)
	}
	walk(queue[0], 0)
	return depths
}

// webpCanonicalCodes assigns the canonical codes to the lengths, as the lossless specification says.
func webpCanonicalCodes(lengths []int) []uint32 {
	maxLen := slices.Max(lengths)
	count := make([]int, maxLen+2)
	for _, l := range lengths {
		count[l]++
	}
	count[0] = 0
	next := make([]uint32, maxLen+2)
	code := uint32(0)
	for l := 1; l <= maxLen; l++ {
		code = (code + uint32(count[l-1])) << 1
		next[l] = code
	}
	codes := make([]uint32, len(lengths))
	for s, l := range lengths {
		if l > 0 {
			codes[s] = next[l]
			next[l]++
		}
	}
	return codes
}

// writeBitsMSB writes a code most significant bit first.
func (w *webpBitWriter) writeBitsMSB(code uint32, length int) {
	for i := length - 1; i >= 0; i-- {
		w.put(code>>i&1, 1)
	}
}

// emit writes a symbol with the code.
func (c *webpPrefixCode) emit(w *webpBitWriter, symbol int) {
	switch {
	case c.single:
	case c.simple != nil:
		if len(c.simple) == 2 && symbol == c.simple[1] {
			w.put(1, 1)
		} else if len(c.simple) == 2 {
			w.put(0, 1)
		}
	default:
		w.writeBitsMSB(c.codes[symbol], c.lengths[symbol])
	}
}

// webpCodeLengthOrder is the order the code length code lengths are written in.
var webpCodeLengthOrder = [19]int{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// writeWebPPrefixCode writes the prefix code for the symbols of used (sorted, distinct) in an alphabet of the given size and returns it.
func writeWebPPrefixCode(w *webpBitWriter, rng *webpRNG, alphabet int, used []int) *webpPrefixCode {
	allSmall := true
	for _, s := range used {
		if s >= 256 {
			allSmall = false
		}
	}

	beyondAlphabet := len(used) > 0 && used[len(used)-1] >= alphabet // only a simple code can carry such a symbol
	if len(used) <= 2 && allSmall && (rng.chance(70) || beyondAlphabet) {
		// simple code
		symbols := slices.Clone(used)
		if len(symbols) == 0 {
			symbols = []int{0}
		}
		if len(symbols) == 2 && rng.chance(50) {
			symbols[0], symbols[1] = symbols[1], symbols[0]
		}
		w.put(1, 1)
		w.put(uint32(len(symbols)-1), 1)
		first8 := symbols[0] > 1 || rng.chance(30)
		if first8 {
			w.put(1, 1)
			w.put(uint32(symbols[0]), 8)
		} else {
			w.put(0, 1)
			w.put(uint32(symbols[0]), 1)
		}
		if len(symbols) == 2 {
			w.put(uint32(symbols[1]), 8)
		}
		return &webpPrefixCode{simple: symbols, single: len(symbols) == 1}
	}

	if len(used) == 0 {
		used = []int{0}
	}

	lengths := make([]int, alphabet)
	if len(used) == 1 {
		lengths[used[0]] = 1 + rng.intn(15)
	} else {
		for s, l := range webpHuffmanLengths(rng, used, 15) {
			lengths[s] = l
		}
	}

	writeWebPCodeLengths(w, rng, lengths)

	code := &webpPrefixCode{lengths: lengths}
	if len(used) == 1 {
		code.single = true
	} else {
		code.codes = webpCanonicalCodes(lengths)
	}
	return code
}

type webpLengthToken struct {
	symbol int
	extra  int
	nbits  int
}

// writeWebPCodeLengths writes the normal prefix code of the lengths: the code length code, then the lengths in it.
func writeWebPCodeLengths(w *webpBitWriter, rng *webpRNG, lengths []int) {
	last := len(lengths)
	for last > 0 && lengths[last-1] == 0 {
		last--
	}

	tokenize := func(end int) []webpLengthToken {
		var tokens []webpLengthToken
		prev := 8
		for i := 0; i < end; {
			l := lengths[i]
			run := 1
			for i+run < end && lengths[i+run] == l {
				run++
			}
			switch {
			case l == 0 && run >= 11 && rng.chance(80):
				r := min(run, 138)
				tokens = append(tokens, webpLengthToken{18, r - 11, 7})
				i += r
			case l == 0 && run >= 3 && rng.chance(60):
				r := min(run, 10)
				tokens = append(tokens, webpLengthToken{17, r - 3, 3})
				i += r
			case l != 0 && l == prev && run >= 3 && rng.chance(70):
				r := min(run, 6)
				tokens = append(tokens, webpLengthToken{16, r - 3, 2})
				i += r
			default:
				tokens = append(tokens, webpLengthToken{l, 0, 0})
				if l != 0 {
					prev = l
				}
				i++
			}
		}
		return tokens
	}

	if last == 1 && lengths[0] != 0 && rng.chance(25) {
		// a single symbol 0 of length lengths[0]: the maximum symbol 2 + 65535 wraps to 1 in the 16 bits of the decoder, which reads the one
		// token and stops
		w.put(0, 1)
		cl := make([]int, 19)
		cl[lengths[0]] = 1
		numCodes := 19
		for numCodes > 4 && cl[webpCodeLengthOrder[numCodes-1]] == 0 {
			numCodes--
		}
		w.put(uint32(numCodes-4), 4)
		for i := range numCodes {
			w.put(uint32(cl[webpCodeLengthOrder[i]]), 3)
		}
		w.put(1, 1)
		w.put(7, 3)
		w.put(65535, 16)
		return
	}

	useMax := rng.chance(50) && last < len(lengths)
	end := len(lengths)
	if useMax {
		end = last
	}
	tokens := tokenize(end)
	if useMax && len(tokens) < 2 {
		useMax = false
		tokens = tokenize(len(lengths))
	}

	// the code length code
	seen := map[int]bool{}
	for _, t := range tokens {
		seen[t.symbol] = true
	}
	var tokenSymbols []int
	for s := range seen {
		tokenSymbols = append(tokenSymbols, s)
	}
	slices.Sort(tokenSymbols)
	cl := make([]int, 19)
	single := len(tokenSymbols) == 1
	if single {
		cl[tokenSymbols[0]] = 1 + rng.intn(7)
	} else {
		for s, l := range webpHuffmanLengths(rng, tokenSymbols, 7) {
			cl[s] = l
		}
	}

	w.put(0, 1)
	numCodes := 19
	for numCodes > 4 && cl[webpCodeLengthOrder[numCodes-1]] == 0 {
		numCodes--
	}
	w.put(uint32(numCodes-4), 4)
	for i := range numCodes {
		w.put(uint32(cl[webpCodeLengthOrder[i]]), 3)
	}

	if useMax {
		w.put(1, 1)
		k := 0
		for len(tokens)-2 >= 1<<(2+2*k) {
			k++
		}
		w.put(uint32(k), 3)
		w.put(uint32(len(tokens)-2), 2+2*k)
	} else {
		w.put(0, 1)
	}

	var codes []uint32
	if !single {
		codes = webpCanonicalCodes(cl)
	}
	for _, t := range tokens {
		if !single {
			w.writeBitsMSB(codes[t.symbol], cl[t.symbol])
		}
		if t.nbits > 0 {
			w.put(uint32(t.extra), t.nbits)
		}
	}
}

// webpLosslessGen writes lossless streams.
type webpLosslessGen struct {
	rng *webpRNG

	transforms []int // the transform types in the order of the stream
	predBits   int
	colorBits  int
	tableSize  int
	cacheBits  int // of the main image, 0: none
	metaBits   int // 0: none
	numGroups  int
	cachePct   int
	backrefPct int
	maxLength  int

	// hostileDist is a distance prefix symbol of 42 or more, which the specification does not allow, for the extra bits of which the
	// decoder reads more than 32 bits into a 32-bit count; the distances are written so that the wrapped count is the one wanted.
	hostileDist int
}

// webpCopyCode is the prefix symbol and extra bits of a length or a distance code value (at least 1).
func webpCopyCode(v int) (symbol, extra, nbits int) {
	if v <= 4 {
		return v - 1, 0, 0
	}
	d := v - 1
	highest := bits.Len(uint(d)) - 1
	second := d >> (highest - 1) & 1
	nbits = highest - 1
	return 2*highest + second, d & (1<<nbits - 1), nbits
}

// webpPlaneDistance is plane_code_to_distance.
func webpPlaneDistance(width, planeCode int) int {
	if planeCode > 120 {
		return planeCode - 120
	}
	o := losslessDistanceMap[planeCode-1]
	return max(1, int(o[0])+int(o[1])*width)
}

type webpLosslessToken struct {
	kind      int // 0 literal, 1 backref, 2 cache
	argb      uint32
	length    int
	planeCode int
	key       int
	group     int

	hostile bool // a distance written as hostileDist
	dextra  uint32
}

// subImage writes an image of the sub-image roles (no transforms, no meta prefix codes) that has exactly the pixels given.
func (g *webpLosslessGen) subImage(w *webpBitWriter, width, height int, pixels []uint32) {
	cacheBits := 0
	if g.rng.chance(25) {
		cacheBits = 1 + g.rng.intn(11)
	}
	if cacheBits > 0 {
		w.put(1, 1)
		w.put(uint32(cacheBits), 4)
	} else {
		w.put(0, 1)
	}

	var used [4]map[int]bool
	for i := range used {
		used[i] = map[int]bool{}
	}
	for _, p := range pixels {
		used[0][int(p>>8&0xff)] = true
		used[1][int(p>>16&0xff)] = true
		used[2][int(p&0xff)] = true
		used[3][int(p>>24)] = true
	}
	var codes [4]*webpPrefixCode
	for i := range 4 {
		alphabet := 256
		if i == 0 {
			alphabet = 280
			if cacheBits > 0 {
				alphabet += 1 << cacheBits
			}
		}
		codes[i] = writeWebPPrefixCode(w, g.rng, alphabet, webpSortedKeys(used[i]))
	}
	writeWebPPrefixCode(w, g.rng, 40, nil)
	for _, p := range pixels {
		codes[0].emit(w, int(p>>8&0xff))
		codes[1].emit(w, int(p>>16&0xff))
		codes[2].emit(w, int(p&0xff))
		codes[3].emit(w, int(p>>24))
	}
}

func webpSortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// image writes the main image of the stream: the transforms, the color cache, the meta prefix codes, the prefix codes and the pixels.
func (g *webpLosslessGen) image(w *webpBitWriter, width, height int) {
	rng := g.rng
	xsize := width

	for _, t := range g.transforms {
		w.put(1, 1)
		w.put(uint32(t), 2)
		switch t {
		case 0, 1:
			bitsN := g.predBits
			if t == 1 {
				bitsN = g.colorBits
			}
			w.put(uint32(bitsN-2), 3)
			bw := (xsize + 1<<bitsN - 1) >> bitsN
			bh := (height + 1<<bitsN - 1) >> bitsN
			pixels := make([]uint32, bw*bh)
			for i := range pixels {
				switch {
				case t == 0 && rng.chance(95):
					pixels[i] = rng.next()&0xffff00ff | uint32(rng.intn(14))<<8
				case t == 0:
					pixels[i] = rng.next()
				default:
					pixels[i] = rng.next()
				}
			}
			g.subImage(w, bw, bh, pixels)
		case 2:
		case 3:
			w.put(uint32(g.tableSize-1), 8)
			pixels := make([]uint32, g.tableSize)
			for i := range pixels {
				pixels[i] = rng.next()
			}
			g.subImage(w, g.tableSize, 1, pixels)
			xsize = (xsize + 1<<losslessWidthBits(uint16(g.tableSize)) - 1) >> losslessWidthBits(uint16(g.tableSize))
		}
	}
	w.put(0, 1)

	if g.cacheBits > 0 {
		w.put(1, 1)
		w.put(uint32(g.cacheBits), 4)
	} else {
		w.put(0, 1)
	}

	huffIndex := func(x, y int) int { return 0 }
	numGroups := 1
	if g.metaBits > 0 {
		w.put(1, 1)
		w.put(uint32(g.metaBits-2), 3)
		hx := (xsize + 1<<g.metaBits - 1) >> g.metaBits
		hy := (height + 1<<g.metaBits - 1) >> g.metaBits
		entropy := make([]uint32, hx*hy)
		maxGroup := 0
		for i := range entropy {
			grp := rng.intn(g.numGroups)
			maxGroup = max(maxGroup, grp)
			entropy[i] = uint32(grp)<<8 | uint32(rng.intn(256)) // only the group matters, in bits 8 to 23
			if rng.chance(30) {
				entropy[i] |= uint32(rng.intn(256)) << 24
			}
		}
		numGroups = maxGroup + 1
		g.subImage(w, hx, hy, entropy)
		huffIndex = func(x, y int) int {
			// get_huff_index: the position is a u16 expression that wraps
			position := int(uint16(uint16(y>>g.metaBits)*uint16(hx) + uint16(x>>g.metaBits)))
			return int(entropy[position] >> 8 & 0xffff)
		}
	} else {
		w.put(0, 1)
	}

	// the tokens
	total := xsize * height
	var tokens []webpLosslessToken
	x, y := 0, 0
	for index := 0; index < total; {
		tok := webpLosslessToken{group: huffIndex(x, y)}
		roll := rng.intn(100)
		switch {
		case roll < g.backrefPct && index > 0:
			length := 1 + rng.intn(min(g.maxLength, total-index))
			if rng.chance(10) {
				length = min(total-index, 1+rng.intn(g.maxLength))
			}
			ok := false
			if g.hostileDist != 0 {
				s := g.hostileDist
				eb := (s - 2) >> 1
				offset := uint32(2+s&1) << (eb & 31)
				dist := 1 + rng.intn(min(index, 40))
				low := uint32(dist+120) - offset - 1
				if eb >= 32 || low < 1<<eb {
					tok.kind, tok.length, tok.hostile, tok.dextra = 1, length, true, low
					ok = true
				}
			}
			for i := 0; !ok && i < 12; i++ {
				planeCode := 1 + rng.intn(120+index/4+8)
				if rng.chance(50) {
					planeCode = 1 + rng.intn(30)
				}
				if webpPlaneDistance(xsize, planeCode) <= index {
					tok.kind, tok.length, tok.planeCode = 1, length, planeCode
					ok = true
					break
				}
			}
			if !ok {
				tok.kind, tok.argb = 0, rng.next()
			}
		case roll < g.backrefPct+g.cachePct && g.cacheBits > 0:
			tok.kind, tok.key = 2, rng.intn(1<<g.cacheBits)
		default:
			tok.argb = rng.next()
			if rng.chance(40) {
				tok.argb &= 0x0f0f0f0f // few distinct symbols, to get short codes and simple codes
			}
		}
		tokens = append(tokens, tok)
		if tok.kind == 1 {
			index += tok.length
			x += tok.length
			for x >= xsize {
				x -= xsize
				y++
			}
		} else {
			index++
			x++
			if x >= xsize {
				x = 0
				y++
			}
		}
	}

	// the symbols each prefix code of each group needs
	type usage [5]map[int]bool
	usages := make([]usage, numGroups)
	for i := range usages {
		for j := range 5 {
			usages[i][j] = map[int]bool{}
		}
	}
	for _, t := range tokens {
		u := &usages[t.group]
		switch t.kind {
		case 0:
			u[0][int(t.argb>>8&0xff)] = true
			u[1][int(t.argb>>16&0xff)] = true
			u[2][int(t.argb&0xff)] = true
			u[3][int(t.argb>>24)] = true
		case 1:
			ls, _, _ := webpCopyCode(t.length)
			u[0][256+ls] = true
			if t.hostile {
				u[4][g.hostileDist] = true
			} else {
				ds, _, _ := webpCopyCode(t.planeCode)
				u[4][ds] = true
			}
		case 2:
			u[0][280+t.key] = true
		}
	}
	groups := make([][5]*webpPrefixCode, numGroups)
	for i := range groups {
		for j := range 5 {
			alphabet := []int{280, 256, 256, 256, 40}[j]
			if j == 0 && g.cacheBits > 0 {
				alphabet += 1 << g.cacheBits
			}
			groups[i][j] = writeWebPPrefixCode(w, rng, alphabet, webpSortedKeys(usages[i][j]))
		}
	}

	for _, t := range tokens {
		c := &groups[t.group]
		switch t.kind {
		case 0:
			c[0].emit(w, int(t.argb>>8&0xff))
			c[1].emit(w, int(t.argb>>16&0xff))
			c[2].emit(w, int(t.argb&0xff))
			c[3].emit(w, int(t.argb>>24))
		case 1:
			ls, le, ln := webpCopyCode(t.length)
			c[0].emit(w, 256+ls)
			w.put(uint32(le), ln)
			if t.hostile {
				c[4].emit(w, g.hostileDist)
				eb := (g.hostileDist - 2) >> 1
				w.put(t.dextra, min(eb, 32))
				for left := eb - 32; left > 0; left -= 32 {
					w.put(0, min(left, 32))
				}
				continue
			}
			ds, de, dn := webpCopyCode(t.planeCode)
			c[4].emit(w, ds)
			w.put(uint32(de), dn)
		case 2:
			c[0].emit(w, 280+t.key)
		}
	}
}

// vp8lStream is a lossless stream without header (the data of the alpha chunk of a lossy image) for a width by height image.
func (g *webpLosslessGen) stream(width, height int) []byte {
	w := &webpBitWriter{}
	g.image(w, width, height)
	return w.bytes()
}

// vp8lChunk is the data of a VP8L chunk of a width by height image.
func (g *webpLosslessGen) chunk(width, height int, alphaUsed bool) []byte {
	w := &webpBitWriter{}
	w.put(uint32(width-1), 14)
	w.put(uint32(height-1), 14)
	if alphaUsed {
		w.put(1, 1)
	} else {
		w.put(0, 1)
	}
	w.put(0, 3)
	g.image(w, width, height)
	return append([]byte{0x2f}, w.bytes()...)
}

// randomWebPLossless is a generator with random options.
func randomWebPLossless(rng *webpRNG, maxTransforms int) *webpLosslessGen {
	g := &webpLosslessGen{rng: rng}
	all := []int{0, 1, 2, 3}
	for i := len(all) - 1; i > 0; i-- {
		j := rng.intn(i + 1)
		all[i], all[j] = all[j], all[i]
	}
	n := rng.intn(maxTransforms + 1)
	g.transforms = all[:n]
	if rng.chance(70) {
		// the order of an encoder: subtract green, predictor, cross color, color indexing last is also seen
		slices.SortFunc(g.transforms, func(a, b int) int { return []int{1, 2, 0, 3}[a] - []int{1, 2, 0, 3}[b] })
	}
	g.predBits = 2 + rng.intn(8)
	g.colorBits = 2 + rng.intn(8)
	g.tableSize = 1 + rng.intn(20)
	if rng.chance(20) {
		g.tableSize = 1 + rng.intn(256)
	}
	if rng.chance(40) {
		g.cacheBits = 1 + rng.intn(11)
	}
	if rng.chance(40) {
		g.metaBits = 2 + rng.intn(8)
		g.numGroups = 1 + rng.intn(6)
	}
	g.backrefPct = 10 + rng.intn(50)
	g.cachePct = 10 + rng.intn(30)
	g.maxLength = 1 + rng.intn(300)
	if rng.chance(10) {
		g.maxLength = 4096
	}
	if rng.chance(6) {
		g.hostileDist = []int{40, 42, 60, 66, 70, 100, 150, 200, 255}[rng.intn(9)]
	}
	return g
}

// constantStream is a lossless stream of a width by height image of one color: the five prefix codes have one symbol each, a pixel takes no bit.
func (g *webpLosslessGen) constantStream(w *webpBitWriter, argb uint32) {
	w.put(0, 1) // no transform
	w.put(0, 1) // no color cache
	w.put(0, 1) // no meta prefix codes
	for i, symbol := range []int{int(argb >> 8 & 0xff), int(argb >> 16 & 0xff), int(argb & 0xff), int(argb >> 24)} {
		writeWebPPrefixCode(w, g.rng, []int{280, 256, 256, 256}[i], []int{symbol})
	}
	writeWebPPrefixCode(w, g.rng, 40, nil)
}

// constantChunk is the data of a VP8L chunk of a width by height image of one color.
func (g *webpLosslessGen) constantChunk(width, height int, argb uint32) []byte {
	w := &webpBitWriter{}
	w.put(uint32(width-1), 14)
	w.put(uint32(height-1), 14)
	w.put(1, 1)
	w.put(0, 3)
	g.constantStream(w, argb)
	return append([]byte{0x2f}, w.bytes()...)
}
