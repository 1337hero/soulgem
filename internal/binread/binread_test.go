package binread

import (
	"strings"
	"testing"
)

func TestReadsLittleEndian(t *testing.T) {
	c := New("test", []byte{
		0x01,
		0x02, 0x03,
		0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	})
	if got := c.U8(); got != 1 {
		t.Errorf("U8 = %d", got)
	}
	if got := c.U16(); got != 0x0302 {
		t.Errorf("U16 = %#x", got)
	}
	if got := c.U32(); got != 0x07060504 {
		t.Errorf("U32 = %#x", got)
	}
	if got := c.U64(); got != 0x0f0e0d0c0b0a0908 {
		t.Errorf("U64 = %#x", got)
	}
	if c.Off() != 15 || c.Remaining() != 0 {
		t.Errorf("off = %d, remaining = %d", c.Off(), c.Remaining())
	}
	if err := c.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTruncationFailsInsteadOfPanicking(t *testing.T) {
	c := New("clip", []byte{1, 2, 3})
	c.U32()
	err := c.Err()
	if err == nil {
		t.Fatal("reading 4 bytes from a 3-byte buffer should fail")
	}
	if !strings.Contains(err.Error(), "clip") || !strings.Contains(err.Error(), "offset 0") {
		t.Errorf("error should carry context and offset, got %q", err)
	}
}

func TestErrorIsStickyAndReadsBecomeZero(t *testing.T) {
	c := New("", []byte{0xff, 0xff, 0xff, 0xff})
	c.Skip(10)
	first := c.Err()
	if first == nil {
		t.Fatal("skipping past the end should fail")
	}
	if got := c.U32(); got != 0 {
		t.Errorf("read after failure returned %#x, want 0", got)
	}
	c.Fail("a second problem")
	if c.Err() != first {
		t.Error("the first error should win")
	}
	if c.Off() != 0 {
		t.Errorf("a failed skip should not move the cursor, off = %d", c.Off())
	}
}

func TestCountRejectsImpossibleLengths(t *testing.T) {
	c := New("", make([]byte, 8))
	if got := c.Count(3, 4, "vertex"); got != 0 {
		t.Errorf("Count = %d, want 0 for 3 vertices of 4 bytes in 8 bytes", got)
	}
	err := c.Err()
	if err == nil || !strings.Contains(err.Error(), "vertex count 3") {
		t.Fatalf("err = %v", err)
	}

	c = New("", make([]byte, 8))
	if got := c.Count(-1, 4, "vertex"); got != 0 || c.Err() == nil {
		t.Errorf("a negative count should fail, got %d %v", got, c.Err())
	}

	c = New("", make([]byte, 8))
	if got := c.Count(2, 4, "vertex"); got != 2 || c.Err() != nil {
		t.Errorf("Count = %d, %v; want 2, nil", got, c.Err())
	}
}

func TestSliceReadsAreBounded(t *testing.T) {
	c := New("", make([]byte, 8))
	if got := c.F32s(100); len(got) != 0 {
		t.Errorf("F32s(100) over 8 bytes returned %d values", len(got))
	}
	if c.Err() == nil {
		t.Error("F32s past the end should fail")
	}

	c = New("", make([]byte, 8))
	if got := c.U16s(100); len(got) != 0 || c.Err() == nil {
		t.Errorf("U16s(100) over 8 bytes returned %d values, err %v", len(got), c.Err())
	}
}

func TestStringsCarryTheirLength(t *testing.T) {
	c := New("", append([]byte{4, 0, 0, 0}, "NiIf"...))
	if got := c.SizedString(); got != "NiIf" {
		t.Errorf("SizedString = %q", got)
	}

	c = New("", append([]byte{6}, "name\x00\x00"...))
	if got := c.ShortString(); got != "name" {
		t.Errorf("ShortString = %q, want the NUL padding trimmed", got)
	}

	c = New("", []byte{20, 0, 0, 0, 'a'})
	if got := c.SizedString(); got != "" || c.Err() == nil {
		t.Errorf("a string longer than the file should fail, got %q %v", got, c.Err())
	}
}

func TestAlignIsRelativeToBase(t *testing.T) {
	c := New("", make([]byte, 32))
	c.Seek(7)
	c.Align(3, 4) // 7-3 = 4, already aligned
	if c.Off() != 7 {
		t.Errorf("off = %d, want 7", c.Off())
	}
	c.Seek(8)
	c.Align(3, 4) // 8-3 = 5, next multiple of 4 is 8
	if c.Off() != 11 {
		t.Errorf("off = %d, want 11", c.Off())
	}
}

func TestPeekToleratesShortReads(t *testing.T) {
	c := New("", []byte{1, 2, 3})
	if got := c.Peek(8); len(got) != 3 {
		t.Errorf("Peek(8) returned %d bytes, want 3", len(got))
	}
	if c.Off() != 0 || c.Err() != nil {
		t.Errorf("Peek must not advance or fail: off=%d err=%v", c.Off(), c.Err())
	}
}

func TestHalfDecodesIEEE754Binary16(t *testing.T) {
	cases := []struct {
		bits uint16
		want float64
	}{
		{0x0000, 0},
		{0x8000, 0},  // negative zero
		{0x3c00, 1},  // 1.0
		{0xc000, -2}, // -2.0
		{0x3555, 0.333251953125},
		{0x0001, 5.960464477539063e-08}, // smallest subnormal
	}
	for _, test := range cases {
		if got := Half(test.bits); got != test.want {
			t.Errorf("Half(%#04x) = %v, want %v", test.bits, got, test.want)
		}
	}
}
