package tri

import (
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"testing"
)

// build assembles a synthetic FRTRI003 file. Real head TRIs are Bethesda
// assets; this reproduces the format precisely enough to pin the parser without
// shipping one.
type build struct {
	numVerts    int
	numTris     int
	numUV       int
	flags       int32
	numModVerts int
	verts       []float32
	morphs      []morph
	trailing    []byte
}

type morph struct {
	name   string
	scale  float32
	deltas [][3]int16
}

func (b build) bytes() []byte {
	out := []byte("FRTRI003")
	i32 := func(v int32) { out = binary.LittleEndian.AppendUint32(out, uint32(v)) }
	f32 := func(v float32) { out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v)) }
	i32(int32(b.numVerts))
	i32(int32(b.numTris))
	i32(0) // quads
	i32(0)
	i32(0)
	i32(int32(b.numUV))
	i32(b.flags)
	i32(int32(len(b.morphs)))
	i32(0) // mods
	i32(int32(b.numModVerts))
	for len(out) < 64 {
		out = append(out, 0)
	}
	for _, v := range b.verts {
		f32(v)
	}
	out = append(out, make([]byte, b.numModVerts*12+b.numTris*12)...)
	if b.flags&1 != 0 {
		out = append(out, make([]byte, b.numUV*8+b.numTris*12)...)
	}
	for _, m := range b.morphs {
		name := m.name
		for len(name)%4 != 0 { // real files pad names with NULs
			name += "\x00"
		}
		i32(int32(len(name)))
		out = append(out, name...)
		f32(m.scale)
		for _, delta := range m.deltas {
			for _, component := range delta {
				out = binary.LittleEndian.AppendUint16(out, uint16(component))
			}
		}
	}
	return append(out, b.trailing...)
}

func sample() build {
	return build{
		numVerts: 2, numTris: 1, numUV: 2, flags: 1, numModVerts: 1,
		verts: []float32{0, 1, 2, 3, 4, 5},
		morphs: []morph{
			{name: "Aah", scale: 0.5, deltas: [][3]int16{{2, 4, 6}, {-2, -4, -6}}},
			{name: "BlinkLeft", scale: 0.25, deltas: [][3]int16{{1, 0, -1}, {0, 8, 0}}},
		},
	}
}

func TestParse(t *testing.T) {
	file, err := Parse(sample().bytes())
	if err != nil {
		t.Fatal(err)
	}
	if file.NumVerts != 2 {
		t.Errorf("NumVerts = %d", file.NumVerts)
	}
	if got := file.Verts; len(got) != 6 || got[0] != 0 || got[5] != 5 {
		t.Errorf("Verts = %v", got)
	}
	if want := []string{"Aah", "BlinkLeft"}; !slices.Equal(file.Order, want) {
		t.Errorf("Order = %v, want %v", file.Order, want)
	}
	// Deltas are int16 counts scaled by the morph's own factor.
	if got := file.Morphs["Aah"]; got[0] != [3]float64{1, 2, 3} || got[1] != [3]float64{-1, -2, -3} {
		t.Errorf("Aah deltas = %v", got)
	}
	if got := file.Morphs["BlinkLeft"]; got[1] != [3]float64{0, 2, 0} {
		t.Errorf("BlinkLeft deltas = %v", got)
	}
	if file.Trailing != 0 {
		t.Errorf("Trailing = %d", file.Trailing)
	}
}

func TestParseReportsTrailingBytes(t *testing.T) {
	b := sample()
	b.trailing = []byte{1, 2, 3, 4, 5}
	file, err := Parse(b.bytes())
	if err != nil {
		t.Fatal(err)
	}
	if file.Trailing != 5 {
		t.Errorf("Trailing = %d, want 5", file.Trailing)
	}
}

func TestParseRejectsBadSignature(t *testing.T) {
	data := sample().bytes()
	copy(data, "FRTRI002")
	if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Parse([]byte("short")); err == nil {
		t.Fatal("a file shorter than the header should be rejected")
	}
}

func TestParseRejectsTruncatedFile(t *testing.T) {
	full := sample().bytes()
	// Every truncation must be an error, never a panic or a partial file.
	for cut := 64; cut < len(full); cut += 7 {
		if _, err := Parse(full[:cut]); err == nil {
			t.Fatalf("truncating to %d of %d bytes parsed successfully", cut, len(full))
		}
	}
}

func TestParseRejectsImpossibleCounts(t *testing.T) {
	b := sample()
	b.numVerts = 1 << 20 // more vertices than the file could hold
	if _, err := Parse(b.bytes()); err == nil {
		t.Fatal("an oversized vertex count should be rejected")
	}

	b = sample()
	b.numVerts = -1
	if _, err := Parse(b.bytes()); err == nil {
		t.Fatal("a negative vertex count should be rejected")
	}
}
