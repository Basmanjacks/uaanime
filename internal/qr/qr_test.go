package qr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden is one reference symbol read from testdata: the payload it was built
// from and the expected matrix, one string of '0'/'1' per row.
type golden struct {
	payload string
	rows    []string
}

func loadGolden(t *testing.T, version int) golden {
	t.Helper()
	name := filepath.Join("testdata", fmt.Sprintf("v%d.txt", version))
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	g := golden{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# payload: "):
			g.payload = strings.TrimPrefix(line, "# payload: ")
		case strings.HasPrefix(line, "#"):
		default:
			g.rows = append(g.rows, line)
		}
	}
	if g.payload == "" || len(g.rows) == 0 {
		t.Fatalf("%s: no payload or no matrix", name)
	}
	return g
}

// TestGoldenMatrices compares the whole matrix, module by module, against
// symbols produced by segno (see the header of every testdata file). Structural
// checks alone would not catch a wrong Reed-Solomon remainder, a wrong mask
// choice or wrong format information — every one of those still looks like a
// well-formed QR code.
func TestGoldenMatrices(t *testing.T) {
	for version := minVersion; version <= maxVersion; version++ {
		g := loadGolden(t, version)
		m, err := Encode(g.payload)
		if err != nil {
			t.Fatalf("v%d: Encode(%d bytes): %v", version, len(g.payload), err)
		}
		if m.Version() != version {
			t.Fatalf("v%d: got version %d for a %d-byte payload", version, m.Version(), len(g.payload))
		}
		if m.Size() != len(g.rows) {
			t.Fatalf("v%d: size %d, want %d", version, m.Size(), len(g.rows))
		}
		diff := 0
		for y, row := range g.rows {
			if len(row) != m.Size() {
				t.Fatalf("v%d: golden row %d has %d modules, want %d", version, y, len(row), m.Size())
			}
			for x, c := range row {
				if m.At(x, y) != (c == '1') {
					diff++
					if diff <= 5 {
						t.Errorf("v%d: module (%d,%d) = %v, want %v", version, x, y, m.At(x, y), c == '1')
					}
				}
			}
		}
		if diff > 0 {
			t.Errorf("v%d: %d of %d modules differ from the golden matrix\ngot:\n%s",
				version, diff, m.Size()*m.Size(), render(m))
		}
	}
}

func TestVersionTableIsConsistent(t *testing.T) {
	for v := minVersion; v <= maxVersion; v++ {
		info := versions[v]
		if got := info.blocks * (info.dataPerBlock + info.ecPerBlock); got != info.total {
			t.Errorf("v%d: %d blocks × (%d data + %d ec) = %d, table says total %d",
				v, info.blocks, info.dataPerBlock, info.ecPerBlock, got, info.total)
		}
	}
	// Byte-mode capacities at level L, ISO/IEC 18004 Table 7.
	want := [maxVersion + 1]int{1: 17, 2: 32, 3: 53, 4: 78, 5: 106, 6: 134}
	for v := minVersion; v <= maxVersion; v++ {
		if got := byteCapacity(v); got != want[v] {
			t.Errorf("v%d: byteCapacity = %d, want %d", v, got, want[v])
		}
	}
}

// TestFormatBitsMatchBCH recomputes the format information from the BCH(15,5)
// code so the hardcoded table cannot drift into a typo.
func TestFormatBitsMatchBCH(t *testing.T) {
	const eccL = 0b01 // error-correction level indicator for L (Table 12)
	for mask := range 8 {
		data := uint16(eccL<<3 | mask)
		rem := data
		for range 10 {
			rem = rem<<1 ^ (rem>>9)*0x537
		}
		want := (data<<10 | rem&0x3ff) ^ 0x5412
		if formatBitsL[mask] != want {
			t.Errorf("mask %d: table has %#04x, BCH gives %#04x", mask, formatBitsL[mask], want)
		}
	}
}

func TestVersionBoundaries(t *testing.T) {
	cases := []struct {
		n    int
		want int // 0 = must not fit
	}{
		{1, 1}, {17, 1}, {18, 2}, {32, 2}, {33, 3}, {53, 3}, {54, 4},
		{78, 4}, {79, 5}, {106, 5}, {107, 6}, {134, 6}, {135, 0}, {5000, 0},
	}
	for _, c := range cases {
		m, err := Encode(strings.Repeat("a", c.n))
		switch {
		case c.want == 0:
			if !errors.Is(err, ErrTooLong) {
				t.Errorf("%d bytes: err = %v, want ErrTooLong", c.n, err)
			}
		case err != nil:
			t.Errorf("%d bytes: unexpected error %v", c.n, err)
		case m.Version() != c.want:
			t.Errorf("%d bytes: version %d, want %d", c.n, m.Version(), c.want)
		}
	}
}

// TestEncodeIsByteMode checks that the length limit counts bytes, not runes:
// the Ukrainian remote URL may carry a non-ASCII mDNS label.
func TestEncodeIsByteMode(t *testing.T) {
	text := strings.Repeat("я", 9) // 18 bytes, 9 runes
	m, err := Encode(text)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if m.Version() != 2 {
		t.Errorf("version %d, want 2 (18 bytes does not fit into v1)", m.Version())
	}
}

func TestStructure(t *testing.T) {
	for version := minVersion; version <= maxVersion; version++ {
		g := loadGolden(t, version)
		m, err := Encode(g.payload)
		if err != nil {
			t.Fatalf("v%d: %v", version, err)
		}
		size := m.Size()
		if want := 17 + 4*version; size != want {
			t.Fatalf("v%d: size %d, want %d", version, size, want)
		}
		// Finder patterns, centre module of each.
		for _, c := range [][2]int{{3, 3}, {size - 4, 3}, {3, size - 4}} {
			for dy := -4; dy <= 4; dy++ {
				for dx := -4; dx <= 4; dx++ {
					x, y := c[0]+dx, c[1]+dy
					if x < 0 || y < 0 || x >= size || y >= size {
						continue
					}
					ring := max(abs(dx), abs(dy))
					if want := ring != 2 && ring != 4; m.At(x, y) != want {
						t.Fatalf("v%d: finder at (%d,%d) module (%d,%d) = %v, want %v",
							version, c[0], c[1], x, y, m.At(x, y), want)
					}
				}
			}
		}
		// Timing patterns alternate between the finders.
		for i := 8; i < size-8; i++ {
			if m.At(i, 6) != (i%2 == 0) || m.At(6, i) != (i%2 == 0) {
				t.Fatalf("v%d: timing broken at %d", version, i)
			}
		}
		if !m.At(8, size-8) {
			t.Errorf("v%d: dark module at (8,%d) is light", version, size-8)
		}
		// At is safe outside the symbol so a renderer can sweep the quiet zone.
		if m.At(-1, 0) || m.At(0, -1) || m.At(size, 0) || m.At(0, size) {
			t.Errorf("v%d: At outside the symbol returned dark", version)
		}
	}
}

func TestReedSolomonKnownVector(t *testing.T) {
	// The worked example from ISO/IEC 18004 Annex I.2: "01234567" in numeric
	// mode, version 1-M, gives these 16 data codewords and 10 EC codewords.
	data := []byte{0x10, 0x20, 0x0c, 0x56, 0x61, 0x80, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11, 0xec, 0x11}
	want := []byte{0xa5, 0x24, 0xd4, 0xc1, 0xed, 0x36, 0xc7, 0x87, 0x2c, 0x55}
	got := rsEncode(data, len(want))
	if len(got) != len(want) {
		t.Fatalf("got %d EC codewords, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("EC codewords = % x, want % x", got, want)
		}
	}
}

func TestGaloisFieldTables(t *testing.T) {
	for i := 1; i < 256; i++ {
		if gfExp[gfLog[byte(i)]] != byte(i) {
			t.Fatalf("gfExp/gfLog are not inverse at %d", i)
		}
	}
	for i := 1; i < 256; i++ {
		a := byte(i)
		inv := gfExp[255-gfLog[a]]
		if gfMul(a, inv) != 1 {
			t.Fatalf("gfMul(%#02x, %#02x) = %#02x, want 0x01", a, inv, gfMul(a, inv))
		}
	}
	if gfMul(0, 0x1f) != 0 || gfMul(0x1f, 0) != 0 || gfMul(1, 0x1f) != 0x1f {
		t.Error("gfMul does not respect 0 and 1")
	}
}

// render draws the matrix the way a terminal shows it, with a four-module quiet
// zone, so a human can point a phone at the output of
// `go test -v -run TestScanWithAPhone ./internal/qr/`.
func render(m Matrix) string {
	const quiet = 4
	var b strings.Builder
	for y := -quiet; y < m.Size()+quiet; y++ {
		for x := -quiet; x < m.Size()+quiet; x++ {
			if m.At(x, y) {
				b.WriteString("██")
			} else {
				b.WriteString("  ")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestScanWithAPhone(t *testing.T) {
	m, err := Encode("http://uaanime.local:8765/r/deadbeef")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	t.Logf("version %d, %d modules\n%s", m.Version(), m.Size(), render(m))
}
