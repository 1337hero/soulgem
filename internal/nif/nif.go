// Package nif implements the subset of game LE/SSE NIF needed by Soulgem's
// character pipeline.
//
// A NIF is a flat array of typed blocks that reference each other by index. The
// readers here are split along that grain: header.go turns the file into a
// block table, node.go builds the scene graph, geometry.go and skin.go read the
// two shape encodings (SSE's BSTriShape and LE's NiTriShape), and shader.go
// reads materials. Every read goes through a bounds-checked cursor, so a
// truncated or corrupt asset produces an error rather than a panic.
package nif

import (
	"fmt"
	"os"

	"soulgem/internal/binread"
	"soulgem/internal/mathutil"
)

type BoneWeight struct {
	Bone   string
	Weight float64
}

type Shape struct {
	Name           string
	Positions      []mathutil.Vec3
	Normals        []mathutil.Vec3
	UVs            [][2]float64
	Colors         [][4]float64
	Triangles      [][3]uint16
	Textures       []string
	AlphaFlags     *uint16
	AlphaThreshold uint8
	ShaderType     any
	Transform      *mathutil.Mat4
	Skinned        bool
	Specular       mathutil.Vec3
	Glossiness     float64
	SkinWeights    [][]BoneWeight
	BoneMatrices   map[string]mathutil.Mat4
}

func newShape() *Shape {
	return &Shape{
		Specular:     mathutil.Vec3{1, 1, 1},
		Glossiness:   80,
		BoneMatrices: make(map[string]mathutil.Mat4),
	}
}

type Node struct {
	Parent    string
	Transform mathutil.Transform
}

type File struct {
	Skeleton   map[string]mathutil.Mat4
	Stream     uint32
	BlockTypes []string
	Shapes     []*Shape

	NodeGlobals map[string]mathutil.Mat4
	NodeTree    map[string]Node
	NodeOrder   []string

	strings      []string
	blockOffsets []int
	data         []byte
	nodeName     map[int]string
	// parent and nodeTF index the scene graph by block, and are what the
	// skinning readers resolve bone globals against.
	parent map[int]int
	nodeTF map[int]mathutil.Transform
}

func Open(path string, skeleton map[string]mathutil.Mat4) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := Parse(data, skeleton)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

func Parse(data []byte, skeleton map[string]mathutil.Mat4) (*File, error) {
	f := &File{
		Skeleton:    skeleton,
		data:        data,
		NodeGlobals: make(map[string]mathutil.Mat4),
		NodeTree:    make(map[string]Node),
		nodeName:    make(map[int]string),
		parent:      make(map[int]int),
		nodeTF:      make(map[int]mathutil.Transform),
	}
	if err := f.readHeader(); err != nil {
		return nil, err
	}
	if err := f.readNodes(); err != nil {
		return nil, err
	}
	if err := f.readShapes(); err != nil {
		return nil, err
	}
	return f, nil
}

// block returns a cursor positioned at block i, refusing indices the file does
// not have. Block references come straight out of the asset, so this is the
// choke point that keeps a corrupt index from indexing a Go slice.
func (f *File) block(index int, what string) (*binread.Cursor, error) {
	if index < 0 || index >= len(f.blockOffsets) {
		return nil, fmt.Errorf("%s references block %d, file has %d", what, index, len(f.blockOffsets))
	}
	c := binread.New(fmt.Sprintf("block %d (%s)", index, f.BlockTypes[index]), f.data)
	c.Seek(f.blockOffsets[index])
	if err := c.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

// typedBlock is block plus an assertion about the block's declared type.
func (f *File) typedBlock(index int, want, what string) (*binread.Cursor, error) {
	c, err := f.block(index, what)
	if err != nil {
		return nil, err
	}
	if f.BlockTypes[index] != want {
		return nil, fmt.Errorf("%s: expected %s, got %s", what, want, f.BlockTypes[index])
	}
	return c, nil
}

// str resolves a string-table index, failing the cursor rather than panicking
// when the file names a string it does not contain.
func (f *File) str(c *binread.Cursor, index int32) string {
	if index < 0 {
		return ""
	}
	if int(index) >= len(f.strings) {
		c.Fail("string index %d, table has %d", index, len(f.strings))
		return ""
	}
	return f.strings[index]
}
