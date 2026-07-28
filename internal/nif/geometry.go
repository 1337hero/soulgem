package nif

import (
	"soulgem/internal/binread"
	"soulgem/internal/mathutil"
)

// shapeTypes are the block types that carry renderable geometry. The first two
// are SSE's packed vertex layout; NiTriShape is LE's separate-arrays layout.
var shapeTypes = map[string]bool{
	"BSTriShape": true, "BSDynamicTriShape": true, "NiTriShape": true,
}

// vertexBlock is SSE's interleaved vertex buffer, decoded into parallel arrays.
type vertexBlock struct {
	positions []mathutil.Vec3
	uvs       [][2]float64
	normals   []mathutil.Vec3
	colors    [][4]float64
	weights   [][4]float64
	bones     [][4]uint8
}

// readVertexBlock decodes numVerts vertices whose layout is described by the
// packed vertex descriptor: the low nibble is the stride, bits 44+ say which
// attributes are present. The stride is re-checked per vertex, which is what
// catches a descriptor that does not match the data.
func readVertexBlock(c *binread.Cursor, desc uint64, numVerts int) (vertexBlock, error) {
	flags := (desc >> 44) & 0x7ff
	stride := int(desc&0xf) * 4
	hasPos, hasUV, hasNrm := flags&1 != 0, flags&2 != 0, flags&8 != 0
	hasTan := flags&0x18 == 0x18
	hasCol, hasSkin, hasEye := flags&0x20 != 0, flags&0x40 != 0, flags&0x100 != 0
	var out vertexBlock
	if stride == 0 {
		c.Fail("vertex descriptor %#x declares a zero stride", desc)
		return out, c.Err()
	}
	for range c.Count(numVerts, stride, "vertex") {
		start := c.Off()
		if hasPos {
			out.positions = append(out.positions, vec3(c))
			c.Skip(4) // bitangent X
		}
		if hasUV {
			out.uvs = append(out.uvs, [2]float64{c.Half(), c.Half()})
		}
		if hasNrm {
			// Normals are unit bytes; the fourth is unused.
			out.normals = append(out.normals, mathutil.Vec3{
				float64(c.U8())/127.5 - 1, float64(c.U8())/127.5 - 1, float64(c.U8())/127.5 - 1,
			})
			c.Skip(1)
		}
		if hasTan {
			c.Skip(4)
		}
		if hasCol {
			out.colors = append(out.colors, [4]float64{
				float64(c.U8()) / 255, float64(c.U8()) / 255,
				float64(c.U8()) / 255, float64(c.U8()) / 255,
			})
		}
		if hasSkin {
			out.weights = append(out.weights, [4]float64{
				c.Half(), c.Half(), c.Half(), c.Half(),
			})
			out.bones = append(out.bones, [4]uint8{c.U8(), c.U8(), c.U8(), c.U8()})
		}
		if hasEye {
			c.Skip(4)
		}
		if c.Failed() {
			return out, c.Err()
		}
		if used := c.Off() - start; used != stride {
			c.Fail("stride mismatch: computed %d, declared %d (flags %#x)", used, stride, flags)
			return out, c.Err()
		}
	}
	return out, c.Err()
}

func readTriangles(c *binread.Cursor, numTris int) [][3]uint16 {
	out := make([][3]uint16, 0, c.Count(numTris, 6, "triangle"))
	for range cap(out) {
		out = append(out, [3]uint16{c.U16(), c.U16(), c.U16()})
	}
	if c.Failed() {
		return nil
	}
	return out
}

// readShapes decodes every geometry block in the file.
func (f *File) readShapes() error {
	for i, blockType := range f.BlockTypes {
		if !shapeTypes[blockType] {
			continue
		}
		c, err := f.block(i, "shape")
		if err != nil {
			return err
		}
		shape := newShape()
		name, tf := f.readAVObject(c)
		shape.Name = name
		m := f.chainTransform(i, tf)
		shape.Transform = &m

		var shaderRef, alphaRef int
		if blockType == "NiTriShape" {
			shaderRef, alphaRef, err = f.readLEShape(c, shape)
		} else {
			shaderRef, alphaRef, err = f.readSSEShape(c, shape, blockType)
		}
		if err != nil {
			return err
		}
		if shaderRef >= 0 {
			if err := f.readShader(shaderRef, shape); err != nil {
				return err
			}
		}
		if alphaRef >= 0 {
			if err := f.readAlpha(alphaRef, shape); err != nil {
				return err
			}
		}
		f.Shapes = append(f.Shapes, shape)
	}
	return nil
}

// readSSEShape reads BSTriShape / BSDynamicTriShape: bounding sphere, refs, a
// packed vertex descriptor, then the interleaved vertex and index buffers.
func (f *File) readSSEShape(c *binread.Cursor, shape *Shape, blockType string) (shaderRef, alphaRef int, err error) {
	c.Skip(16) // bounding sphere
	skinRef := int(c.I32())
	shaderRef, alphaRef = int(c.I32()), int(c.I32())
	desc := c.U64()
	numTris, numVerts := int(c.U16()), int(c.U16())
	dataSize := c.U32()
	if err := c.Err(); err != nil {
		return 0, 0, err
	}
	var block vertexBlock
	if dataSize > 0 {
		block, err = readVertexBlock(c, desc, numVerts)
		if err != nil {
			return 0, 0, err
		}
		shape.Positions, shape.UVs = block.positions, block.uvs
		shape.Normals, shape.Colors = block.normals, block.colors
		shape.Triangles = readTriangles(c, numTris)
	}
	// A dynamic shape overrides its positions with a full-precision copy, used
	// for meshes the game morphs at runtime.
	if blockType == "BSDynamicTriShape" {
		dynamicSize := c.U32()
		if dynamicSize == 0 {
			dynamicSize = c.U32()
		}
		if err := c.Err(); err != nil {
			return 0, 0, err
		}
		if int(dynamicSize) != numVerts*16 {
			c.Fail("dynamic vertex block is %d bytes, expected %d", dynamicSize, numVerts*16)
			return 0, 0, c.Err()
		}
		shape.Positions = shape.Positions[:0]
		for range c.Count(numVerts, 16, "dynamic vertex") {
			shape.Positions = append(shape.Positions, vec3(c))
			c.Skip(4)
		}
	}
	if err := c.Err(); err != nil {
		return 0, 0, err
	}
	if skinRef >= 0 {
		shape.Skinned = true
		if err := f.readSSESkin(skinRef, shape, block.weights, block.bones); err != nil {
			return 0, 0, err
		}
	}
	return shaderRef, alphaRef, nil
}

// readLEShape reads NiTriShape, whose geometry lives in a separate
// NiTriShapeData block.
func (f *File) readLEShape(c *binread.Cursor, shape *Shape) (shaderRef, alphaRef int, err error) {
	dataRef := int(c.I32())
	skinRef := int(c.I32())
	c.Skip(8 * c.Count(int(c.U32()), 8, "material"))
	c.Skip(4) // active material
	c.Skip(1) // has shader
	shaderRef, alphaRef = int(c.I32()), int(c.I32())
	if err := c.Err(); err != nil {
		return 0, 0, err
	}
	shape.Skinned = skinRef >= 0
	if dataRef >= 0 {
		if err := f.readTriShapeData(dataRef, shape); err != nil {
			return 0, 0, err
		}
	}
	if skinRef >= 0 {
		if err := f.readLESkin(skinRef, shape); err != nil {
			return 0, 0, err
		}
	}
	return shaderRef, alphaRef, nil
}

// readTriShapeData reads LE's NiTriShapeData: parallel arrays, each preceded by
// a byte saying whether it is present.
func (f *File) readTriShapeData(ref int, shape *Shape) error {
	c, err := f.typedBlock(ref, "NiTriShapeData", "shape data")
	if err != nil {
		return err
	}
	c.Skip(4) // group id
	numVerts := int(c.U16())
	c.Skip(2) // keep flags, compress flags
	if c.U8() != 0 {
		for range c.Count(numVerts, 12, "vertex") {
			shape.Positions = append(shape.Positions, vec3(c))
		}
	}
	vectorFlags := c.U16()
	numUVSets := int(vectorFlags & 1)
	c.Skip(4) // material CRC
	if c.U8() != 0 {
		for range c.Count(numVerts, 12, "normal") {
			shape.Normals = append(shape.Normals, vec3(c))
		}
		if vectorFlags&0x1000 != 0 {
			c.Skip(24 * c.Count(numVerts, 24, "tangent")) // tangents and bitangents
		}
	}
	c.Skip(16) // bounding sphere
	if c.U8() != 0 {
		for range c.Count(numVerts, 16, "color") {
			shape.Colors = append(shape.Colors,
				[4]float64{c.F32(), c.F32(), c.F32(), c.F32()})
		}
	}
	for set := range numUVSets {
		uvs := make([][2]float64, c.Count(numVerts, 8, "UV"))
		for i := range uvs {
			uvs[i] = [2]float64{c.F32(), c.F32()}
		}
		if set == 0 {
			shape.UVs = uvs
		}
	}
	c.Skip(2) // consistency flags
	c.Skip(4) // additional data
	numTris := int(c.U16())
	c.Skip(4) // triangle index count
	if c.U8() != 0 {
		shape.Triangles = append(shape.Triangles, readTriangles(c, numTris)...)
	}
	return c.Err()
}

func (f *File) readAlpha(ref int, shape *Shape) error {
	c, err := f.block(ref, "alpha property")
	if err != nil {
		return err
	}
	f.readNamed(c)
	flags := c.U16()
	threshold := c.U8()
	if err := c.Err(); err != nil {
		return err
	}
	shape.AlphaFlags = &flags
	shape.AlphaThreshold = threshold
	return nil
}
