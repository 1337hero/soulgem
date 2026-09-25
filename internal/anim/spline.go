package anim

import (
	"math"

	"soulgem/internal/binread"
)

// maxDegree is the highest B-spline degree the weight buffer holds. Havok never
// emits more than cubic, and a corrupt degree byte would otherwise index past
// the fixed-size weight array.
const maxDegree = 4

// Track sub-types, from the per-track mask bits.
const (
	identity = iota
	static
	dynamic
)

type vecTrack struct {
	axes   [3][]float64
	knots  []byte
	degree int
}

// sample evaluates the track at a frame within its block. An axis with a single
// control point is constant and needs no basis evaluation.
func (t vecTrack) sample(frame float64) [3]float64 {
	var out [3]float64
	span := -1
	var weights [maxDegree + 1]float64
	for axis, values := range t.axes {
		if len(values) == 1 {
			out[axis] = values[0]
			continue
		}
		if span < 0 {
			span = findKnotSpan(t.degree, frame, len(values), t.knots)
			weights = bsplineWeights(span, t.degree, frame, t.knots)
		}
		for i := 0; i <= t.degree && span-i >= 0; i++ {
			out[axis] += values[span-i] * weights[i]
		}
	}
	return out
}

type quatTrack struct {
	points [][4]float64
	knots  []byte
	degree int
}

func (t quatTrack) sample(frame float64) [4]float64 {
	if t.knots == nil {
		return t.points[0]
	}
	span := findKnotSpan(t.degree, frame, len(t.points), t.knots)
	weights := bsplineWeights(span, t.degree, frame, t.knots)
	var q [4]float64
	for i := 0; i <= t.degree && span-i >= 0; i++ {
		p := t.points[span-i]
		for k := range 4 {
			q[k] += p[k] * weights[i]
		}
	}
	length := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	if length == 0 {
		length = 1
	}
	for i := range q {
		q[i] /= length
	}
	return q
}

type track struct {
	pos   vecTrack
	rot   quatTrack
	scale vecTrack
}

// findKnotSpan locates the knot interval containing value. The knot vector is
// non-decreasing bytes, so the search is a plain bisection; the iteration bound
// is there because a corrupt, non-monotonic vector would otherwise spin.
func findKnotSpan(degree int, value float64, numPoints int, knots []byte) int {
	if numPoints >= len(knots) {
		return max(0, len(knots)-1)
	}
	if value >= float64(knots[numPoints]) {
		return numPoints - 1
	}
	low, high := degree, numPoints
	mid := (low + high) / 2
	for range len(knots) + 1 {
		if mid < 0 || mid+1 >= len(knots) {
			return max(0, min(mid, numPoints-1))
		}
		if value >= float64(knots[mid]) && value < float64(knots[mid+1]) {
			return mid
		}
		if value < float64(knots[mid]) {
			high = mid
		} else {
			low = mid
		}
		next := (low + high) / 2
		if next == mid {
			break
		}
		mid = next
	}
	return max(0, min(mid, numPoints-1))
}

// bsplineWeights evaluates the Cox-de Boor basis functions at frame.
func bsplineWeights(span, degree int, frame float64, knots []byte) [maxDegree + 1]float64 {
	n := [maxDegree + 1]float64{1}
	for i := 1; i <= degree; i++ {
		for j := i - 1; j >= 0; j-- {
			if span-j < 0 || span+i-j >= len(knots) {
				continue
			}
			width := float64(int(knots[span+i-j]) - int(knots[span-j]))
			if width == 0 {
				continue
			}
			a := (frame - float64(knots[span-j])) / width
			tmp := n[j] * a
			n[j+1] += n[j] - tmp
			n[j] = tmp
		}
	}
	return n
}

// readQuat40 decodes Havok's 40-bit quaternion: three 12-bit components plus
// two bits saying which component was dropped and one sign bit. It occupies
// five bytes but is read as a 64-bit word, so the tail of a block may supply
// fewer than eight bytes.
func readQuat40(c *binread.Cursor) [4]float64 {
	var raw [8]byte
	copy(raw[:], c.Peek(8))
	c.Skip(5)
	v := uint64(raw[0]) | uint64(raw[1])<<8 | uint64(raw[2])<<16 |
		uint64(raw[3])<<24 | uint64(raw[4])<<32 | uint64(raw[5])<<40 |
		uint64(raw[6])<<48 | uint64(raw[7])<<56
	const mask = (1 << 11) - 1
	const fractal = 0.000345436
	components := [3]uint64{v & 0xfff, (v >> 12) & 0xfff, (v >> 24) & 0xfff}
	x := (float64(components[0]) - mask) * fractal
	y := (float64(components[1]) - mask) * fractal
	z := (float64(components[2]) - mask) * fractal
	dropped := (v >> 36) & 3
	sign := 1.0
	if v>>38&1 != 0 {
		sign = -1
	}
	return assembleQuat(x, y, z, dropped, sign)
}

// readQuat48 decodes Havok's 48-bit quaternion: three 15-bit components in
// three little-endian words. The top bit of the first two words indexes the
// dropped component; the top bit of the third is its sign.
func readQuat48(c *binread.Cursor) [4]float64 {
	words := [3]uint64{uint64(c.U16()), uint64(c.U16()), uint64(c.U16())}
	const mask = (1 << 14) - 1
	const fractal = 0.000043161
	x := (float64(words[0]&0x7fff) - mask) * fractal
	y := (float64(words[1]&0x7fff) - mask) * fractal
	z := (float64(words[2]&0x7fff) - mask) * fractal
	dropped := (words[0] >> 15) | (words[1]>>15)<<1
	sign := 1.0
	if words[2]>>15 != 0 {
		sign = -1
	}
	return assembleQuat(x, y, z, dropped, sign)
}

func assembleQuat(x, y, z float64, dropped uint64, sign float64) [4]float64 {
	w := math.Sqrt(max(0, 1-(x*x+y*y+z*z))) * sign
	var out [4]float64
	src := [3]float64{x, y, z}
	next := 0
	for i := range 4 {
		if uint64(i) == dropped {
			out[i] = w
			continue
		}
		out[i] = src[next]
		next++
	}
	return out
}

func readQuat(quantization int, c *binread.Cursor) [4]float64 {
	switch quantization {
	case 3:
		return readQuat40(c)
	case 4:
		return readQuat48(c)
	case 7:
		values := c.F32s(4)
		if c.Failed() {
			return [4]float64{}
		}
		return [4]float64{values[0], values[1], values[2], values[3]}
	default:
		c.Fail("unsupported rotation quantization %d", quantization)
		return [4]float64{}
	}
}

// blockReader decodes one spline block: numTracks transform tracks laid out
// back to back, each aligned relative to the start of the block.
type blockReader struct {
	c    *binread.Cursor
	base int
}

// subType reads the two mask bits describing one axis of one track.
func subType(mask byte, bit int) int {
	switch {
	case mask&(1<<bit) != 0:
		return static
	case mask&(1<<(bit+4)) != 0:
		return dynamic
	default:
		return identity
	}
}

// readDegree reads and validates a spline degree.
func (b *blockReader) readDegree() int {
	degree := int(b.c.U8())
	if degree < 1 || degree > maxDegree {
		b.c.Fail("spline degree %d is outside 1..%d", degree, maxDegree)
		return 0
	}
	return degree
}

func (b *blockReader) readKnots(numItems, degree int) []byte {
	return b.c.Copy(b.c.Count(numItems+degree+2, 1, "knot"))
}

// readVec reads a position or scale track. Its three axes are independently
// identity, constant, or spline-encoded, but they share one knot vector.
func (b *blockReader) readVec(mask byte, quantization int, identityValue float64) vecTrack {
	types := [3]int{subType(mask, 0), subType(mask, 1), subType(mask, 2)}
	spline := types[0] == dynamic || types[1] == dynamic || types[2] == dynamic
	if !spline {
		var axes [3][]float64
		for axis, kind := range types {
			if kind == static {
				axes[axis] = []float64{b.c.F32()}
			} else {
				axes[axis] = []float64{identityValue}
			}
		}
		return vecTrack{axes: axes}
	}

	numItems := int(b.c.U16())
	degree := b.readDegree()
	knots := b.readKnots(numItems, degree)
	b.c.Align(b.base, 4)
	var axes [3][]float64
	var ranges [3][2]float64
	for axis, kind := range types {
		switch kind {
		case dynamic:
			ranges[axis] = [2]float64{b.c.F32(), b.c.F32()}
			axes[axis] = make([]float64, b.c.Count(numItems, 1, "control point")+1)
		case static:
			axes[axis] = []float64{b.c.F32()}
		default:
			axes[axis] = []float64{identityValue}
		}
	}
	if b.c.Failed() {
		return vecTrack{}
	}
	// Control points are stored interleaved by item, each axis quantized to
	// one or two bytes across its own min/max range.
	for item := 0; item <= numItems; item++ {
		for axis, kind := range types {
			if kind != dynamic {
				continue
			}
			var unit float64
			if quantization == 0 {
				unit = float64(b.c.U8()) / 255
			} else {
				unit = float64(b.c.U16()) / 65535
			}
			minimum, maximum := ranges[axis][0], ranges[axis][1]
			axes[axis][item] = minimum + (maximum-minimum)*unit
		}
	}
	b.c.Align(b.base, 4)
	return vecTrack{axes: axes, knots: knots, degree: degree}
}

func (b *blockReader) readRot(mask byte, quantization int) quatTrack {
	if mask&0xf0 != 0 { // spline
		numItems := int(b.c.U16())
		degree := b.readDegree()
		knots := b.readKnots(numItems, degree)
		switch quantization {
		case 4, 6:
			b.c.Align(b.base, 2)
		case 2, 7:
			b.c.Align(b.base, 4)
		}
		points := make([][4]float64, 0, b.c.Count(numItems, 5, "rotation")+1)
		for range cap(points) {
			points = append(points, readQuat(quantization, b.c))
			if b.c.Failed() {
				return quatTrack{points: [][4]float64{{0, 0, 0, 1}}}
			}
		}
		return quatTrack{points: points, knots: knots, degree: degree}
	}
	if mask&0xf != 0 { // constant
		return quatTrack{points: [][4]float64{readQuat(quantization, b.c)}}
	}
	return quatTrack{points: [][4]float64{{0, 0, 0, 1}}}
}

// decodeBlock decodes one block's transform tracks. The block opens with a
// four-byte mask per transform track plus a byte per float track, then the
// track payloads.
func decodeBlock(data []byte, base, numTracks, numFloatTracks int) ([]track, error) {
	c := binread.New("spline block", data)
	c.Seek(base)
	if err := c.Err(); err != nil {
		return nil, err
	}
	masks := make([][4]byte, c.Count(numTracks, 4, "track mask"))
	for i := range masks {
		copy(masks[i][:], c.Bytes(4))
	}
	c.Skip(numFloatTracks)
	c.Align(base, 4)
	if err := c.Err(); err != nil {
		return nil, err
	}

	b := &blockReader{c: c, base: base}
	tracks := make([]track, 0, len(masks))
	for _, mask := range masks {
		quantization, posMask, rotMask, scaleMask := mask[0], mask[1], mask[2], mask[3]
		posQuant := int(quantization & 3)
		rotQuant := int((quantization>>2)&0xf) + 2
		scaleQuant := int((quantization >> 6) & 3)
		pos := b.readVec(posMask, posQuant, 0)
		rot := b.readRot(rotMask, rotQuant)
		c.Align(base, 4)
		scale := b.readVec(scaleMask, scaleQuant, 1)
		if err := c.Err(); err != nil {
			return nil, err
		}
		tracks = append(tracks, track{pos: pos, rot: rot, scale: scale})
	}
	return tracks, c.Err()
}
