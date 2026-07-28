// Package binread is a bounds-checked cursor over a byte slice.
//
// The asset parsers read counts and offsets straight out of files that are not
// under our control, so every read has to survive truncation and corruption.
// Checking each read at the call site would bury the format in error handling,
// so the cursor keeps a sticky error instead: once a read runs off the end
// every later read is a no-op returning the zero value, and the caller checks
// Err once at a natural boundary. Errors carry the cursor's name and offset,
// which is what makes a bad file diagnosable.
package binread

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

type Cursor struct {
	data []byte
	off  int
	err  error
	// name labels errors, so a failure says which file or block it came from.
	name string
}

func New(name string, data []byte) *Cursor { return &Cursor{data: data, name: name} }

func (c *Cursor) Off() int       { return c.off }
func (c *Cursor) Remaining() int { return max(0, len(c.data)-c.off) }
func (c *Cursor) Err() error     { return c.err }
func (c *Cursor) Failed() bool   { return c.err != nil }

// Fail records an error at the current offset if none is recorded yet.
func (c *Cursor) Fail(format string, args ...any) { c.fail(format, args...) }

func (c *Cursor) fail(format string, args ...any) {
	if c.err != nil {
		return
	}
	c.err = fmt.Errorf("%s at offset %d: %s", c.name, c.off, fmt.Sprintf(format, args...))
}

// Seek moves to an absolute offset.
func (c *Cursor) Seek(off int) {
	if c.err != nil {
		return
	}
	if off < 0 || off > len(c.data) {
		c.fail("seek to %d is outside the %d-byte file", off, len(c.data))
		return
	}
	c.off = off
}

// Skip advances by n bytes. A negative n rewinds.
func (c *Cursor) Skip(n int) { c.Seek(c.off + n) }

// Align advances so the offset is a multiple of n relative to base.
func (c *Cursor) Align(base, n int) {
	if remainder := (c.off - base) % n; remainder != 0 {
		c.Skip(n - remainder)
	}
}

// take reserves n bytes and returns them, or nil once the cursor has failed.
func (c *Cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 {
		c.fail("negative read of %d bytes", n)
		return nil
	}
	if n > len(c.data)-c.off {
		c.fail("read of %d bytes runs %d past the end of the file", n, n-(len(c.data)-c.off))
		return nil
	}
	out := c.data[c.off : c.off+n]
	c.off += n
	return out
}

// Count validates a length read from the file before it is used to size an
// allocation: n items of itemSize bytes each must fit in what is left. It
// returns 0 once the cursor has failed, so loops collapse instead of spinning.
func (c *Cursor) Count(n int, itemSize int, what string) int {
	if c.err != nil {
		return 0
	}
	if n < 0 {
		c.fail("negative %s count %d", what, n)
		return 0
	}
	if itemSize > 0 && n > c.Remaining()/itemSize {
		c.fail("%s count %d needs %d bytes, only %d remain", what, n, n*itemSize, c.Remaining())
		return 0
	}
	return n
}

// zeroWord backs reads made after the cursor has failed, so the fixed-width
// readers decode zeros instead of each testing for a short read.
var zeroWord [8]byte

// word reads n bytes, or n zero bytes once the cursor has failed.
func (c *Cursor) word(n int) []byte {
	if b := c.take(n); b != nil {
		return b
	}
	return zeroWord[:n]
}

func (c *Cursor) U8() uint8   { return c.word(1)[0] }
func (c *Cursor) U16() uint16 { return binary.LittleEndian.Uint16(c.word(2)) }
func (c *Cursor) U32() uint32 { return binary.LittleEndian.Uint32(c.word(4)) }
func (c *Cursor) U64() uint64 { return binary.LittleEndian.Uint64(c.word(8)) }

func (c *Cursor) I16() int16 { return int16(c.U16()) }
func (c *Cursor) I32() int32 { return int32(c.U32()) }

// F32 widens to float64 because every consumer computes in float64.
func (c *Cursor) F32() float64 { return float64(math.Float32frombits(c.U32())) }

func (c *Cursor) F32s(n int) []float64 {
	out := make([]float64, c.Count(n, 4, "float"))
	for i := range out {
		out[i] = c.F32()
	}
	return out
}

func (c *Cursor) U16s(n int) []uint16 {
	out := make([]uint16, c.Count(n, 2, "uint16"))
	for i := range out {
		out[i] = c.U16()
	}
	return out
}

// Bytes returns a view into the underlying data; it is not copied.
func (c *Cursor) Bytes(n int) []byte { return c.take(n) }

// Peek returns up to n bytes at the cursor without advancing and without
// failing on a short read, for fields whose encoded width is smaller than the
// word they are decoded from.
func (c *Cursor) Peek(n int) []byte {
	if c.err != nil {
		return nil
	}
	return c.data[c.off:min(c.off+n, len(c.data))]
}

// Copy is Bytes with a copy, for values outliving the source buffer.
func (c *Cursor) Copy(n int) []byte {
	return append([]byte(nil), c.Bytes(n)...)
}

// SizedString reads a uint32 length followed by that many bytes.
func (c *Cursor) SizedString() string { return string(c.Bytes(int(c.U32()))) }

// ShortString reads a uint8 length followed by that many bytes, dropping the
// trailing NUL these fields carry.
func (c *Cursor) ShortString() string {
	return strings.TrimRight(string(c.Bytes(int(c.U8()))), "\x00")
}

// Half decodes an IEEE 754 binary16.
func Half(h uint16) float64 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h & 0x3ff)
	var bits uint32
	switch exp {
	case 0:
		if frac == 0 {
			bits = sign
		} else {
			// Normalize subnormal half.
			e := int32(-14)
			for frac&0x400 == 0 {
				frac <<= 1
				e--
			}
			frac &= 0x3ff
			bits = sign | uint32(e+127)<<23 | frac<<13
		}
	case 0x1f:
		bits = sign | 0x7f800000 | frac<<13
	default:
		bits = sign | (exp+112)<<23 | frac<<13
	}
	return float64(math.Float32frombits(bits))
}

func (c *Cursor) Half() float64 { return Half(c.U16()) }
