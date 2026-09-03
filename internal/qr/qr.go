// Package qr encodes text into a QR Code matrix.
//
// The scope is deliberately narrow: byte mode, error-correction level L,
// versions 1..6. That is all the remote address needs, and it keeps the whole
// encoder inside the standard library — a QR code on one screen does not
// justify a dependency.
//
// References are to ISO/IEC 18004 (QR Code bar code symbology specification).
package qr

import (
	"errors"
	"fmt"
)

// ErrTooLong reports that the payload does not fit into the largest supported
// symbol (version 6 at level L holds 134 bytes).
var ErrTooLong = errors.New("text does not fit into a QR code")

// Matrix is a finished QR symbol: a square grid of modules without the quiet
// zone. Rendering the quiet zone is the caller's job, because how much margin
// fits depends on the screen.
type Matrix struct {
	version int
	size    int
	dark    []bool
	// fixed marks function patterns and format information: modules that carry
	// no data and are never masked.
	fixed []bool
}

// Size returns the side of the symbol in modules (17 + 4×version).
func (m Matrix) Size() int { return m.size }

// Version returns the QR version (1..6) chosen for the payload.
func (m Matrix) Version() int { return m.version }

// At reports whether the module at column x, row y is dark. Coordinates outside
// the symbol are light, so a renderer may sweep the quiet zone with the same
// loop it uses for the symbol.
func (m Matrix) At(x, y int) bool {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return false
	}
	return m.dark[y*m.size+x]
}

// Encode builds the smallest version 1..6 symbol that holds text.
func Encode(text string) (Matrix, error) {
	data := []byte(text)
	version, ok := chooseVersion(len(data))
	if !ok {
		return Matrix{}, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLong, len(data), byteCapacity(maxVersion))
	}

	m := newMatrix(version)
	m.drawFunctionPatterns()
	m.placeData(finalCodewords(data, version))
	m.applyBestMask()
	return m, nil
}

// --- parameters -------------------------------------------------------------

const (
	minVersion = 1
	maxVersion = 6

	modeByte = 0x4 // mode indicator for 8-bit byte mode (§7.4.1)
)

// versionInfo holds the level-L error-correction characteristics of one
// version, taken from ISO/IEC 18004 Table 9 (total codewords) and Table 13
// (number of blocks, data and EC codewords per block). Versions 1..5 at level L
// are a single block; version 6 at level L is two blocks of 68 data + 18 EC
// codewords, 172 codewords in total.
type versionInfo struct {
	total        int // codewords in the whole symbol, data + error correction
	blocks       int
	dataPerBlock int
	ecPerBlock   int
}

var versions = [maxVersion + 1]versionInfo{
	1: {total: 26, blocks: 1, dataPerBlock: 19, ecPerBlock: 7},
	2: {total: 44, blocks: 1, dataPerBlock: 34, ecPerBlock: 10},
	3: {total: 70, blocks: 1, dataPerBlock: 55, ecPerBlock: 15},
	4: {total: 100, blocks: 1, dataPerBlock: 80, ecPerBlock: 20},
	5: {total: 134, blocks: 1, dataPerBlock: 108, ecPerBlock: 26},
	6: {total: 172, blocks: 2, dataPerBlock: 68, ecPerBlock: 18},
}

// alignCenter is the single alignment-pattern centre for versions 2..6
// (Table E.1). The other coordinates the table lists for these versions all
// collide with a finder pattern and are therefore not drawn; version 1 has no
// alignment pattern at all.
var alignCenter = [maxVersion + 1]int{2: 18, 3: 22, 4: 26, 5: 30, 6: 34}

// formatBitsL is the 15-bit format information for level L and masks 0..7
// (Table 25): the 5-bit format data extended by the BCH(15,5) code and XORed
// with 0x5412. The values are recomputed from scratch in the tests.
var formatBitsL = [8]uint16{0x77c4, 0x72f3, 0x7daa, 0x789d, 0x662f, 0x6318, 0x6c41, 0x6976}

// dataCodewords is the number of data (non-EC) codewords in a symbol.
func dataCodewords(version int) int {
	v := versions[version]
	return v.blocks * v.dataPerBlock
}

// byteCapacity is how many payload bytes fit into a version: all data bits
// minus the 4-bit mode indicator and the 8-bit character count.
func byteCapacity(version int) int {
	return (dataCodewords(version)*8 - 4 - 8) / 8
}

func chooseVersion(n int) (int, bool) {
	for v := minVersion; v <= maxVersion; v++ {
		if n <= byteCapacity(v) {
			return v, true
		}
	}
	return 0, false
}

// --- GF(256) and Reed-Solomon ----------------------------------------------

// gfPoly is the field-generating polynomial x⁸+x⁴+x³+x²+1 prescribed for QR
// codes (§7.5.2).
const gfPoly = 0x11d

var (
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	x := 1
	for i := range 255 {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= gfPoly
		}
	}
	// The exponent table is stored twice so that gfMul can add two logarithms
	// (each at most 254) without reducing them modulo 255.
	for i := 255; i < len(gfExp); i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGenerator returns the generator polynomial (x-α⁰)(x-α¹)…(x-α^(degree-1)),
// highest-degree coefficient first.
func rsGenerator(degree int) []byte {
	g := []byte{1}
	for i := range degree {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c                    // multiply by x
			next[j+1] ^= gfMul(c, gfExp[i]) // and by α^i
		}
		g = next
	}
	return g
}

// rsEncode returns the ecLen error-correction codewords for data.
func rsEncode(data []byte, ecLen int) []byte {
	gen := rsGenerator(ecLen)
	rem := make([]byte, ecLen)
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[ecLen-1] = 0
		for i, g := range gen[1:] {
			rem[i] ^= gfMul(g, factor)
		}
	}
	return rem
}

// --- bit stream -------------------------------------------------------------

type bitWriter struct {
	bytes []byte
	nbits int
}

func (w *bitWriter) write(value uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.nbits%8 == 0 {
			w.bytes = append(w.bytes, 0)
		}
		if value>>uint(i)&1 == 1 {
			w.bytes[w.nbits/8] |= 1 << uint(7-w.nbits%8)
		}
		w.nbits++
	}
}

// finalCodewords turns the payload into the interleaved data and
// error-correction codewords of the symbol, ready for placement.
func finalCodewords(data []byte, version int) []byte {
	info := versions[version]
	capacity := dataCodewords(version) * 8

	var w bitWriter
	w.write(modeByte, 4)
	// The character count is 8 bits wide for byte mode in versions 1..9
	// (Table 3), which covers the whole supported range.
	w.write(uint32(len(data)), 8)
	for _, b := range data {
		w.write(uint32(b), 8)
	}
	// Terminator: up to four zero bits, fewer if the symbol is nearly full.
	w.write(0, min(4, capacity-w.nbits))
	if rest := w.nbits % 8; rest != 0 {
		w.write(0, 8-rest)
	}
	// Pad codewords alternate 11101100 / 00010001 (§7.4.10).
	pad := []byte{0xec, 0x11}
	for i := 0; len(w.bytes) < dataCodewords(version); i++ {
		w.bytes = append(w.bytes, pad[i%2])
	}

	blocks := make([][]byte, info.blocks)
	ecBlocks := make([][]byte, info.blocks)
	for i := range blocks {
		blocks[i] = w.bytes[i*info.dataPerBlock : (i+1)*info.dataPerBlock]
		ecBlocks[i] = rsEncode(blocks[i], info.ecPerBlock)
	}

	// Interleave: one codeword from each block in turn, data blocks first,
	// then the error-correction blocks (§7.6).
	out := make([]byte, 0, info.total)
	for i := range info.dataPerBlock {
		for _, b := range blocks {
			out = append(out, b[i])
		}
	}
	for i := range info.ecPerBlock {
		for _, b := range ecBlocks {
			out = append(out, b[i])
		}
	}
	return out
}

// --- matrix construction ----------------------------------------------------

func newMatrix(version int) Matrix {
	size := 17 + 4*version
	return Matrix{
		version: version,
		size:    size,
		dark:    make([]bool, size*size),
		fixed:   make([]bool, size*size),
	}
}

func (m *Matrix) set(x, y int, dark, fixed bool) {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return
	}
	m.dark[y*m.size+x] = dark
	if fixed {
		m.fixed[y*m.size+x] = true
	}
}

// reserve marks a module as carrying no data without touching its colour.
func (m *Matrix) reserve(x, y int) {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return
	}
	m.fixed[y*m.size+x] = true
}

func (m *Matrix) drawFunctionPatterns() {
	// Timing patterns first; the finders overwrite the ends of both lines.
	for i := range m.size {
		m.set(6, i, i%2 == 0, true)
		m.set(i, 6, i%2 == 0, true)
	}

	// Finder patterns with their separators, keyed on the centre module.
	for _, c := range [][2]int{{3, 3}, {m.size - 4, 3}, {3, m.size - 4}} {
		m.drawFinder(c[0], c[1])
	}

	if m.version >= 2 {
		m.drawAlignment(alignCenter[m.version], alignCenter[m.version])
	}

	// Format information, including the dark module: reserved now so it is
	// never masked, written once the mask is known.
	for i := range 9 {
		m.reserve(8, i)
		m.reserve(i, 8)
	}
	for i := range 8 {
		m.reserve(m.size-1-i, 8)
		m.reserve(8, m.size-1-i)
	}
}

// drawFinder draws a 7×7 finder pattern plus its one-module separator, ringed
// outwards from the centre: rings 0-1 dark, ring 2 light, ring 3 dark, ring 4
// is the separator.
func (m *Matrix) drawFinder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			ring := max(abs(dx), abs(dy))
			m.set(cx+dx, cy+dy, ring != 2 && ring != 4, true)
		}
	}
}

// drawAlignment draws a 5×5 alignment pattern: dark centre, light ring, dark
// border.
func (m *Matrix) drawAlignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			m.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1, true)
		}
	}
}

// placeData walks the symbol in two-module-wide columns, right to left,
// alternating upwards and downwards, skipping the vertical timing column
// (§7.7.3). Modules left over after the codewords (the remainder bits) stay
// light but are still masked.
func (m *Matrix) placeData(codewords []byte) {
	bit := 0
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // column 6 is the timing pattern
		}
		for vert := range m.size {
			for j := range 2 {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = m.size - 1 - vert
				}
				if m.fixed[y*m.size+x] || bit >= len(codewords)*8 {
					continue
				}
				m.dark[y*m.size+x] = codewords[bit/8]>>uint(7-bit%8)&1 == 1
				bit++
			}
		}
	}
}

// maskCondition reports whether the module at column x, row y is flipped by the
// given mask pattern (Table 10).
func maskCondition(mask, x, y int) bool {
	switch mask {
	case 0:
		return (y+x)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (y+x)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return y*x%2+y*x%3 == 0
	case 6:
		return (y*x%2+y*x%3)%2 == 0
	default:
		return ((y+x)%2+y*x%3)%2 == 0
	}
}

// applyMask XORs the mask over every data module. It is its own inverse.
func (m *Matrix) applyMask(mask int) {
	for y := range m.size {
		for x := range m.size {
			if !m.fixed[y*m.size+x] && maskCondition(mask, x, y) {
				m.dark[y*m.size+x] = !m.dark[y*m.size+x]
			}
		}
	}
}

// placeFormat writes both copies of the format information for level L and the
// given mask (§7.9). Bit 0 is the least significant bit of the 15-bit word.
func (m *Matrix) placeFormat(mask int) {
	bits := formatBitsL[mask]
	bit := func(i int) bool { return bits>>uint(i)&1 == 1 }

	// The dark module belongs to the format information (§7.9.1), so like the
	// format bits it appears only after the mask has been chosen.
	m.set(8, m.size-8, true, true)

	// Copy 1, around the top-left finder.
	for i := range 6 {
		m.set(8, i, bit(i), true)
	}
	m.set(8, 7, bit(6), true)
	m.set(8, 8, bit(7), true)
	m.set(7, 8, bit(8), true)
	for i := 9; i < 15; i++ {
		m.set(14-i, 8, bit(i), true)
	}

	// Copy 2, split between the bottom-left and top-right finders.
	for i := range 8 {
		m.set(m.size-1-i, 8, bit(i), true)
	}
	for i := 8; i < 15; i++ {
		m.set(8, m.size-15+i, bit(i), true)
	}
}

// applyBestMask tries all eight masks and keeps the one with the lowest
// penalty score (§7.8.3). The format information is written only afterwards:
// §7.8 evaluates the result of masking the encoding region, and the format
// modules are not part of it.
func (m *Matrix) applyBestMask() {
	best, bestScore := 0, 0
	for mask := range 8 {
		m.applyMask(mask)
		score := m.penalty()
		m.applyMask(mask) // the mask is its own inverse
		if mask == 0 || score < bestScore {
			best, bestScore = mask, score
		}
	}
	m.applyMask(best)
	m.placeFormat(best)
}

// Penalty weights from Table 11.
const (
	penaltyN1 = 3
	penaltyN2 = 3
	penaltyN3 = 40
	penaltyN4 = 10
)

func (m *Matrix) penalty() int {
	score := 0

	// N1: runs of five or more modules of the same colour, in rows and columns.
	// N3: the finder-like 1:1:3:1:1 pattern with four light modules on one side.
	for i := range m.size {
		row := make([]bool, m.size)
		col := make([]bool, m.size)
		for j := range m.size {
			row[j] = m.At(j, i)
			col[j] = m.At(i, j)
		}
		score += lineRunPenalty(row) + lineRunPenalty(col)
		score += finderLikePenalty(row) + finderLikePenalty(col)
	}

	// N2: every 2×2 block of one colour.
	for y := range m.size - 1 {
		for x := range m.size - 1 {
			c := m.At(x, y)
			if m.At(x+1, y) == c && m.At(x, y+1) == c && m.At(x+1, y+1) == c {
				score += penaltyN2
			}
		}
	}

	// N4: deviation of the dark-module share from 50 %, in whole steps of 5 %.
	// The integer form is exactly floor(|dark/total·100 − 50| / 5).
	dark := 0
	for _, d := range m.dark {
		if d {
			dark++
		}
	}
	total := m.size * m.size
	score += penaltyN4 * (abs(dark*100-50*total) / (5 * total))

	return score
}

func lineRunPenalty(line []bool) int {
	score, run := 0, 1
	for i := 1; i < len(line); i++ {
		if line[i] == line[i-1] {
			run++
			continue
		}
		if run >= 5 {
			score += penaltyN1 + run - 5
		}
		run = 1
	}
	if run >= 5 {
		score += penaltyN1 + run - 5
	}
	return score
}

// finderCore is the 1:1:3:1:1 dark:light:dark:light:dark proportion a scanner
// could mistake for a finder pattern.
var finderCore = [7]bool{true, false, true, true, true, false, true}

// finderLikePenalty scores every occurrence of finderCore that is preceded or
// followed by a four-module light area.
//
// Two readings of Table 11 have to be pinned down here, because the table does
// not spell them out and encoders differ:
//   - Outside the symbol counts as light. The quiet zone is mandatory and at
//     least four modules wide, so a core at the very edge is exactly as
//     confusable as one in the middle.
//   - A core scores once, not once per side: the table pays for "existence of
//     the pattern", and a core flanked by light on both sides is still one
//     pattern.
func finderLikePenalty(line []bool) int {
	score := 0
	for i := 0; i+len(finderCore) <= len(line); {
		if [7]bool(line[i:i+7]) != finderCore {
			i++
			continue
		}
		if allLight(line, i-4, i) || allLight(line, i+7, i+11) {
			score += penaltyN3
			i += 7
			continue
		}
		// Not enough light around this core; the next one may start inside it,
		// four modules along (dark light dark [dark] dark light dark).
		i += 4
	}
	return score
}

// allLight reports whether every module of line in [from, to) is light,
// treating positions outside the symbol as light.
func allLight(line []bool, from, to int) bool {
	for i := max(from, 0); i < min(to, len(line)); i++ {
		if line[i] {
			return false
		}
	}
	return true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
