// Package tri parses game FaceGen FRTRI003 morph files.
package tri

import (
	"fmt"
	"strings"

	"soulgem/internal/binread"
	"soulgem/internal/mathutil"
)

type File struct {
	NumVerts int
	Verts    []float32
	Morphs   map[string][]mathutil.Vec3
	Order    []string
	Trailing int
}

// header is the fixed 64-byte FRTRI003 header. Only the counts the morph reader
// needs are named; the rest is padding the format reserves.
type header struct {
	numVerts    int
	numTris     int
	numUV       int
	flags       int32
	numMorphs   int
	numModVerts int
}

func readHeader(c *binread.Cursor) header {
	c.Seek(8) // past the "FRTRI003" signature
	h := header{numVerts: int(c.I32()), numTris: int(c.I32())}
	c.Skip(12) // quads and two reserved counts
	h.numUV = int(c.I32())
	h.flags = c.I32()
	h.numMorphs = int(c.I32())
	c.Skip(4) // mods
	h.numModVerts = int(c.I32())
	c.Seek(64)
	return h
}

func Parse(data []byte) (*File, error) {
	if len(data) < 64 || string(data[:8]) != "FRTRI003" {
		return nil, fmt.Errorf("unsupported TRI signature %q", data[:min(8, len(data))])
	}
	c := binread.New("TRI", data)
	h := readHeader(c)

	out := &File{NumVerts: h.numVerts, Morphs: make(map[string][]mathutil.Vec3)}
	out.Verts = make([]float32, 3*c.Count(h.numVerts, 12, "vertex"))
	for i := range out.Verts {
		out.Verts[i] = float32(c.F32())
	}
	c.Skip(h.numModVerts*12 + h.numTris*12)
	if h.flags&1 != 0 {
		c.Skip(h.numUV*8 + h.numTris*12)
	}
	// Each morph is a name, a scale, and one int16 triple per vertex.
	for range c.Count(h.numMorphs, 4+2+6*h.numVerts, "morph") {
		name := strings.TrimRight(c.SizedString(), "\x00")
		scale := c.F32()
		deltas := make([]mathutil.Vec3, c.Count(h.numVerts, 6, "morph delta"))
		for i := range deltas {
			deltas[i] = mathutil.Vec3{
				float64(c.I16()) * scale,
				float64(c.I16()) * scale,
				float64(c.I16()) * scale,
			}
		}
		if c.Failed() {
			return nil, c.Err()
		}
		out.Morphs[name] = deltas
		out.Order = append(out.Order, name)
	}
	if err := c.Err(); err != nil {
		return nil, err
	}
	out.Trailing = len(data) - c.Off()
	return out, nil
}
