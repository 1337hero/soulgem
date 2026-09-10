package nif

import (
	"fmt"
	"strings"

	"soulgem/internal/binread"
)

// supportedVersion is the NIF version, 20.2.0.7. Nothing else in the
// pipeline has ever been fed anything older.
const supportedVersion = 0x14020007

// readHeader turns the leading header into the block table: per-block type
// names and byte offsets, plus the string table blocks index into.
func (f *File) readHeader() error {
	newline := -1
	for i, b := range f.data {
		if b == '\n' {
			newline = i
			break
		}
	}
	if newline < 0 || !strings.Contains(string(f.data[:newline]), "20.2.0.7") {
		return fmt.Errorf("unsupported NIF header %q", f.data[:max(0, newline)])
	}
	c := binread.New("NIF header", f.data)
	c.Seek(newline + 1)
	if v := c.U32(); v != supportedVersion {
		if err := c.Err(); err != nil {
			return err
		}
		return fmt.Errorf("unsupported NIF version %#x", v)
	}
	if endian := c.U8(); endian != 1 {
		if err := c.Err(); err != nil {
			return err
		}
		return fmt.Errorf("unsupported endian %d", endian)
	}
	c.Skip(4) // user version
	numBlocks := int(c.U32())
	f.Stream = c.U32()
	c.ShortString() // author
	c.ShortString() // process script
	c.ShortString() // export script

	types := make([]string, c.Count(int(c.U16()), 5, "block type"))
	for i := range types {
		types[i] = c.SizedString()
	}
	typeIndex := c.U16s(c.Count(numBlocks, 2, "block"))
	sizes := make([]uint32, len(typeIndex))
	for i := range sizes {
		sizes[i] = c.U32()
	}
	numStrings := c.Count(int(c.U32()), 4, "string")
	c.Skip(4) // max string length
	f.strings = make([]string, numStrings)
	for i := range f.strings {
		f.strings[i] = c.SizedString()
	}
	c.Skip(4) // unknown int
	if err := c.Err(); err != nil {
		return err
	}

	f.BlockTypes = make([]string, len(typeIndex))
	for i, index := range typeIndex {
		index &= 0x7fff
		if int(index) >= len(types) {
			return fmt.Errorf("block %d has type index %d, only %d types", i, index, len(types))
		}
		f.BlockTypes[i] = types[index]
	}
	off := c.Off()
	f.blockOffsets = make([]int, len(sizes))
	for i, size := range sizes {
		if off > len(f.data) {
			return fmt.Errorf("block %d starts at %d, past the %d-byte file", i, off, len(f.data))
		}
		f.blockOffsets[i] = off
		off += int(size)
	}
	return nil
}
