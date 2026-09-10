package nif

import (
	"soulgem/internal/binread"
	"soulgem/internal/mathutil"
)

// skinInstance is the bone list every skin block starts with, in both the LE
// and SSE encodings.
type skinInstance struct {
	dataRef      int
	partitionRef int
	boneRefs     []int
}

// readSkinInstance reads NiSkinInstance / BSDismemberSkinInstance. The LE
// variant has no partition of its own, so partitionRef is only meaningful for
// SSE.
func (f *File) readSkinInstance(c *binread.Cursor) skinInstance {
	skin := skinInstance{dataRef: int(c.I32()), partitionRef: int(c.I32())}
	c.Skip(4) // skeleton root
	skin.boneRefs = make([]int, c.Count(int(c.U32()), 4, "bone"))
	for i := range skin.boneRefs {
		skin.boneRefs[i] = int(c.I32())
	}
	return skin
}

// boneTransform reads one NiSkinData bone entry: the bone's bind-pose
// transform, followed by a bounding sphere we ignore.
func (f *File) boneTransform(c *binread.Cursor, boneRef int, shape *Shape) mathutil.Mat4 {
	// A bone's bind transform stores its rotation first, unlike a node's.
	rotation := mat3(c)
	bind := mathutil.Transform{Rotation: rotation, Translation: vec3(c), Scale: c.F32()}
	c.Skip(16) // bounding sphere
	if c.Failed() {
		return mathutil.Mat4{}
	}
	matrix := mathutil.Multiply(f.boneGlobal(boneRef), mathutil.TransformMatrix(bind))
	shape.BoneMatrices[f.nodeName[boneRef]] = matrix
	return matrix
}

// resolveBones maps a skin's bone-reference list to block indices, rejecting
// references the file does not have.
func resolveBones(c *binread.Cursor, boneRefs []int, index int) (int, bool) {
	if index < 0 || index >= len(boneRefs) {
		c.Fail("bone index %d, skin has %d bones", index, len(boneRefs))
		return 0, false
	}
	return boneRefs[index], true
}

// readLESkin applies LE skinning: NiSkinData holds per-bone vertex/weight
// lists, so weights are gathered bone by bone.
func (f *File) readLESkin(skinRef int, shape *Shape) error {
	c, err := f.block(skinRef, "skin instance")
	if err != nil {
		return err
	}
	skin := f.readSkinInstance(c)
	if err := c.Err(); err != nil {
		return err
	}
	if skin.dataRef < 0 {
		return nil
	}
	data, err := f.typedBlock(skin.dataRef, "NiSkinData", "skin data")
	if err != nil {
		return err
	}
	data.Skip(52) // overall transform: 9 rotation + 3 translation + scale
	if n := int(data.U32()); n != len(skin.boneRefs) {
		if err := data.Err(); err != nil {
			return err
		}
		data.Fail("skin data has %d bones, skin instance has %d", n, len(skin.boneRefs))
		return data.Err()
	}
	hasWeights := data.U8() != 0
	influences := make([][]mathutil.Influence, len(shape.Positions))
	shape.SkinWeights = make([][]BoneWeight, len(shape.Positions))
	for bone := range skin.boneRefs {
		boneRef := skin.boneRefs[bone]
		matrix := f.boneTransform(data, boneRef, shape)
		boneName := f.nodeName[boneRef]
		numVerts := data.Count(int(data.U16()), 6, "skin weight")
		if !hasWeights {
			continue
		}
		for range numVerts {
			vertex, weight := int(data.U16()), data.F32()
			if data.Failed() {
				return data.Err()
			}
			if vertex < 0 || vertex >= len(shape.SkinWeights) {
				data.Fail("weight names vertex %d, shape has %d", vertex, len(shape.SkinWeights))
				return data.Err()
			}
			shape.SkinWeights[vertex] = append(shape.SkinWeights[vertex], BoneWeight{boneName, weight})
			influences[vertex] = append(influences[vertex], mathutil.Influence{Matrix: matrix, Weight: weight})
		}
	}
	if err := data.Err(); err != nil {
		return err
	}
	if hasWeights {
		shape.Positions, shape.Normals = mathutil.BakeBindPose(shape.Positions, shape.Normals, influences)
		shape.Transform = nil
	}
	return nil
}

// readSSESkin applies SSE skinning. The weights come from the shape's own
// interleaved vertex buffer (or the partition's copy of it), and NiSkinData is
// read only for the bind-pose matrices.
func (f *File) readSSESkin(skinRef int, shape *Shape, weights [][4]float64, boneIndices [][4]uint8) error {
	c, err := f.block(skinRef, "skin instance")
	if err != nil {
		return err
	}
	if blockType := f.BlockTypes[skinRef]; blockType != "NiSkinInstance" && blockType != "BSDismemberSkinInstance" {
		c.Fail("unexpected skin block %s", blockType)
		return c.Err()
	}
	skin := f.readSkinInstance(c)
	if err := c.Err(); err != nil {
		return err
	}
	if skin.partitionRef < 0 {
		return nil
	}
	partitionWeights, partitionBones, err := f.readPartitions(skin.partitionRef, shape)
	if err != nil {
		return err
	}
	if partitionWeights != nil {
		weights, boneIndices = partitionWeights, partitionBones
	}
	if len(weights) == 0 || skin.dataRef < 0 {
		return nil
	}

	boneMatrices, err := f.readBindMatrices(skin, shape)
	if err != nil {
		return err
	}
	return f.applySSEWeights(c, skin, shape, weights, boneIndices, boneMatrices)
}

func (f *File) readBindMatrices(skin skinInstance, shape *Shape) ([]mathutil.Mat4, error) {
	data, err := f.typedBlock(skin.dataRef, "NiSkinData", "skin data")
	if err != nil {
		return nil, err
	}
	data.Skip(52) // overall transform
	numSkinBones := data.Count(int(data.U32()), 76, "skin bone")
	hasWeights := data.U8() != 0
	boneMatrices := make([]mathutil.Mat4, numSkinBones)
	for bone := range numSkinBones {
		boneRef, ok := resolveBones(data, skin.boneRefs, bone)
		if !ok {
			return nil, data.Err()
		}
		boneMatrices[bone] = f.boneTransform(data, boneRef, shape)
		numVerts := data.Count(int(data.U16()), 6, "skin weight")
		if hasWeights {
			data.Skip(6 * numVerts)
		}
	}
	if err := data.Err(); err != nil {
		return nil, err
	}

	return boneMatrices, nil
}

func (f *File) applySSEWeights(c *binread.Cursor, skin skinInstance, shape *Shape, weights [][4]float64, boneIndices [][4]uint8, boneMatrices []mathutil.Mat4) error {
	influences := make([][]mathutil.Influence, len(shape.Positions))
	shape.SkinWeights = make([][]BoneWeight, len(shape.Positions))
	for vertex := range shape.Positions {
		if vertex >= len(weights) || vertex >= len(boneIndices) {
			c.Fail("shape has %d vertices but only %d skin weights", len(shape.Positions), min(len(weights), len(boneIndices)))
			return c.Err()
		}
		// SSE bone indices are global into the skin's bone list, not relative
		// to the partition's bone palette. Reading them as palette-relative
		// silently attaches vertices to the wrong bones.
		for slot, weight := range weights[vertex] {
			if weight == 0 {
				continue
			}
			bone := int(boneIndices[vertex][slot])
			if bone >= len(boneMatrices) {
				c.Fail("vertex %d references bone %d, skin data has %d", vertex, bone, len(boneMatrices))
				return c.Err()
			}
			boneRef, ok := resolveBones(c, skin.boneRefs, bone)
			if !ok {
				return c.Err()
			}
			shape.SkinWeights[vertex] = append(shape.SkinWeights[vertex],
				BoneWeight{f.nodeName[boneRef], weight})
			influences[vertex] = append(influences[vertex], mathutil.Influence{
				Matrix: boneMatrices[bone], Weight: weight,
			})
		}
	}
	shape.Positions, shape.Normals = mathutil.BakeBindPose(shape.Positions, shape.Normals, influences)
	shape.Transform = nil
	return nil
}

// readPartitions reads NiSkinPartition. Beyond the triangles, SSE repeats the
// whole vertex buffer here, and that copy wins when it is present.
func (f *File) readPartitions(ref int, shape *Shape) ([][4]float64, [][4]uint8, error) {
	c, err := f.block(ref, "skin partition")
	if err != nil {
		return nil, nil, err
	}
	numPartitions := int(c.U32())
	dataSize, vertexSize := int(c.U32()), int(c.U32())
	desc := c.U64()
	if err := c.Err(); err != nil {
		return nil, nil, err
	}
	var weights [][4]float64
	var boneIndices [][4]uint8
	if dataSize > 0 {
		if vertexSize <= 0 {
			c.Fail("partition declares %d bytes of vertices at %d bytes each", dataSize, vertexSize)
			return nil, nil, c.Err()
		}
		block, err := readVertexBlock(c, desc, dataSize/vertexSize)
		if err != nil {
			return nil, nil, err
		}
		if len(block.positions) != 0 {
			shape.Positions = block.positions
		}
		if len(block.uvs) != 0 {
			shape.UVs = block.uvs
		}
		if len(block.normals) != 0 {
			shape.Normals = block.normals
		}
		if len(block.colors) != 0 {
			shape.Colors = block.colors
		}
		if len(block.weights) != 0 {
			weights, boneIndices = block.weights, block.bones
		}
	}
	for range c.Count(numPartitions, 12, "partition") {
		triangles, err := readPartition(c)
		if err != nil {
			return nil, nil, err
		}
		shape.Triangles = append(shape.Triangles, triangles...)
	}
	return weights, boneIndices, c.Err()
}

// readPartition reads one partition and returns its triangles. Everything else
// it carries — the bone palette, the vertex map, the per-vertex weights — is
// redundant with the shape-level data, so it is only skipped past.
func readPartition(c *binread.Cursor) ([][3]uint16, error) {
	numVerts, numTris := int(c.U16()), int(c.U16())
	numBones, numStrips, weightsPerVertex := int(c.U16()), int(c.U16()), int(c.U16())
	c.U16s(numBones) // bone palette
	if c.U8() != 0 {
		c.U16s(numVerts) // vertex map
	}
	if c.U8() != 0 {
		c.Skip(4 * c.Count(numVerts*weightsPerVertex, 4, "partition weight"))
	}
	stripLengths := c.U16s(numStrips)
	var triangles [][3]uint16
	if c.U8() != 0 { // has faces
		if numStrips != 0 {
			total := 0
			for _, n := range stripLengths {
				total += int(n)
			}
			c.Skip(2 * c.Count(total, 2, "strip index"))
		} else {
			triangles = readTriangles(c, numTris)
		}
	}
	if c.U8() != 0 {
		c.Skip(c.Count(numVerts*weightsPerVertex, 1, "bone index"))
	}
	c.Skip(2) // unknown short
	c.Skip(8) // dismember partition flags and body part
	// SSE repeats the triangles in full after the LE-shaped fields; when
	// present that copy is authoritative.
	if repeated := readTriangles(c, numTris); len(repeated) != 0 {
		triangles = repeated
	}
	return triangles, c.Err()
}
