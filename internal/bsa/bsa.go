// Package bsa reads Skyrim Special Edition BSA v105 archives.
package bsa

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Entry struct {
	Offset     uint32
	Size       uint32
	Compressed bool
}

type Archive struct {
	path       string
	embedNames bool
	Files      map[string]Entry
	Order      []string
}

func Open(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var h struct {
		Magic         [4]byte
		Version       uint32
		Offset        uint32
		Flags         uint32
		FolderCount   uint32
		FileCount     uint32
		FolderNameLen uint32
		FileNameLen   uint32
		FileFlags     uint32
	}
	if err := binary.Read(f, binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if string(h.Magic[:]) != "BSA\x00" || h.Version != 105 {
		return nil, fmt.Errorf("unsupported BSA header %q version %d", h.Magic, h.Version)
	}
	type folder struct {
		Hash  uint64
		Count uint32
		Pad   uint32
		Off   uint64
	}
	folders := make([]folder, h.FolderCount)
	if err := binary.Read(f, binary.LittleEndian, &folders); err != nil {
		return nil, err
	}
	type pending struct {
		folder string
		entry  Entry
	}
	var entries []pending
	compressedDefault := h.Flags&0x4 != 0
	for _, folderRecord := range folders {
		var n uint8
		if err := binary.Read(f, binary.LittleEndian, &n); err != nil {
			return nil, err
		}
		name := make([]byte, n)
		if _, err := io.ReadFull(f, name); err != nil {
			return nil, err
		}
		name = bytes.TrimSuffix(name, []byte{0})
		folderName := normalize(string(name))
		for range folderRecord.Count {
			var hash uint64
			var size, off uint32
			if err := binary.Read(f, binary.LittleEndian, &hash); err != nil {
				return nil, err
			}
			if err := binary.Read(f, binary.LittleEndian, &size); err != nil {
				return nil, err
			}
			if err := binary.Read(f, binary.LittleEndian, &off); err != nil {
				return nil, err
			}
			entries = append(entries, pending{folderName, Entry{
				Offset: off, Size: size & 0x3fffffff,
				Compressed: compressedDefault != (size&0x40000000 != 0),
			}})
		}
	}
	nameBlock := make([]byte, h.FileNameLen)
	if _, err := io.ReadFull(f, nameBlock); err != nil {
		return nil, err
	}
	names := bytes.Split(nameBlock, []byte{0})
	a := &Archive{
		path: path, embedNames: h.Flags&0x100 != 0,
		Files: make(map[string]Entry, h.FileCount),
	}
	for i, entry := range entries {
		if i >= len(names) {
			return nil, fmt.Errorf("BSA filename block has %d names for %d entries", len(names), len(entries))
		}
		key := entry.folder + "/" + strings.ToLower(string(names[i]))
		a.Files[key] = entry.entry
		a.Order = append(a.Order, key)
	}
	return a, nil
}

func normalize(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	return strings.ToLower(filepath.ToSlash(path))
}

func (a *Archive) Read(path string) ([]byte, error) {
	entry, ok := a.Files[normalize(path)]
	if !ok {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(a.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data := make([]byte, entry.Size)
	if _, err := f.ReadAt(data, int64(entry.Offset)); err != nil {
		return nil, err
	}
	if a.embedNames {
		if len(data) == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		n := int(data[0])
		if len(data) < n+1 {
			return nil, io.ErrUnexpectedEOF
		}
		data = data[n+1:]
	}
	if !entry.Compressed {
		return data, nil
	}
	if len(data) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	cmd := exec.Command("lz4", "-d", "-c")
	cmd.Stdin = bytes.NewReader(data[4:])
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lz4 %s: %w", path, err)
	}
	return out, nil
}
