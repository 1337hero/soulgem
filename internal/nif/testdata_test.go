package nif

import (
	"encoding/binary"
	"math"
)

// builder assembles a synthetic Skyrim SE NIF. The real ones are Bethesda
// assets that cannot be committed, so the tests build files that exercise the
// same layouts: a node tree, a BSTriShape with an interleaved vertex buffer, a
// dismembered skin with partitions, a lighting shader, and alpha.
type builder struct {
	strings []string
	types   []string
	blocks  []block
}

type block struct {
	kind string
	body []byte
}

func (b *builder) stringIndex(value string) int32 {
	for i, existing := range b.strings {
		if existing == value {
			return int32(i)
		}
	}
	b.strings = append(b.strings, value)
	return int32(len(b.strings) - 1)
}

func (b *builder) add(kind string, body []byte) int {
	b.blocks = append(b.blocks, block{kind: kind, body: body})
	return len(b.blocks) - 1
}

func (b *builder) bytes() []byte {
	for _, blk := range b.blocks {
		known := false
		for _, kind := range b.types {
			known = known || kind == blk.kind
		}
		if !known {
			b.types = append(b.types, blk.kind)
		}
	}
	out := []byte("Gamebryo File Format, Version 20.2.0.7\n")
	w := &writer{out: out}
	w.u32(supportedVersion)
	w.u8(1)   // little endian
	w.u32(12) // user version
	w.u32(uint32(len(b.blocks)))
	w.u32(100) // stream version
	w.shortString("soulgem tests")
	w.shortString("")
	w.shortString("")
	w.u16(uint16(len(b.types)))
	for _, kind := range b.types {
		w.sizedString(kind)
	}
	for _, blk := range b.blocks {
		for i, kind := range b.types {
			if kind == blk.kind {
				w.u16(uint16(i))
				break
			}
		}
	}
	for _, blk := range b.blocks {
		w.u32(uint32(len(blk.body)))
	}
	w.u32(uint32(len(b.strings)))
	w.u32(64) // max string length
	for _, value := range b.strings {
		w.sizedString(value)
	}
	w.u32(0) // unknown
	for _, blk := range b.blocks {
		w.out = append(w.out, blk.body...)
	}
	return w.out
}

// writer is the encoding counterpart of binread.Cursor, for tests only.
type writer struct{ out []byte }

func (w *writer) u8(v uint8)   { w.out = append(w.out, v) }
func (w *writer) u16(v uint16) { w.out = binary.LittleEndian.AppendUint16(w.out, v) }
func (w *writer) u32(v uint32) { w.out = binary.LittleEndian.AppendUint32(w.out, v) }
func (w *writer) u64(v uint64) { w.out = binary.LittleEndian.AppendUint64(w.out, v) }
func (w *writer) i32(v int32)  { w.u32(uint32(v)) }
func (w *writer) f32(v float64) {
	w.u32(math.Float32bits(float32(v)))
}
func (w *writer) f32s(values ...float64) {
	for _, value := range values {
		w.f32(value)
	}
}
func (w *writer) zeros(n int) { w.out = append(w.out, make([]byte, n)...) }
func (w *writer) sizedString(value string) {
	w.u32(uint32(len(value)))
	w.out = append(w.out, value...)
}
func (w *writer) shortString(value string) {
	w.u8(uint8(len(value) + 1))
	w.out = append(w.out, value...)
	w.u8(0)
}

// half encodes the subset of binary16 the fixtures need.
func half(value float64) uint16 {
	bits := math.Float32bits(float32(value))
	sign := uint16(bits>>16) & 0x8000
	exponent := int32((bits>>23)&0xff) - 127
	mantissa := uint16((bits >> 13) & 0x3ff)
	if bits<<1 == 0 {
		return sign
	}
	return sign | uint16(exponent+15)<<10 | mantissa
}

func identityRotation() []float64 { return []float64{1, 0, 0, 0, 1, 0, 0, 0, 1} }

// avObject writes the NiAVObject prefix: name, extra data, controller, flags,
// transform, collision.
func (w *writer) avObject(name int32) {
	w.i32(name)
	w.u32(0) // extra data
	w.i32(-1)
	w.u32(14) // flags
	w.f32s(0, 0, 0)
	w.f32s(identityRotation()...)
	w.f32(1)
	w.i32(-1)
}

func (b *builder) node(name string, children ...int) int {
	w := &writer{}
	w.avObject(b.stringIndex(name))
	w.u32(uint32(len(children)))
	for _, child := range children {
		w.i32(int32(child))
	}
	return b.add("NiNode", w.out)
}

// vertexFlags describes positions, UVs, normals and skin data, at 36 bytes a
// vertex.
const (
	vertexFlags  = 0x4b
	vertexStride = 36
	vertexDesc   = uint64(vertexFlags)<<44 | vertexStride/4
)

type vertex struct {
	position [3]float64
	uv       [2]float64
	normal   [3]uint8
	weights  [4]float64
	bones    [4]uint8
}

func (w *writer) vertex(v vertex) {
	w.f32s(v.position[:]...)
	w.u32(0) // bitangent X
	w.u16(half(v.uv[0]))
	w.u16(half(v.uv[1]))
	w.out = append(w.out, v.normal[0], v.normal[1], v.normal[2], 0)
	for _, weight := range v.weights {
		w.u16(half(weight))
	}
	w.out = append(w.out, v.bones[0], v.bones[1], v.bones[2], v.bones[3])
}

type shapeSpec struct {
	name      string
	vertices  []vertex
	triangles [][3]uint16
	skinRef   int
	shaderRef int
	alphaRef  int
}

func (b *builder) triShape(spec shapeSpec) int {
	w := &writer{}
	w.avObject(b.stringIndex(spec.name))
	w.zeros(16) // bounding sphere
	w.i32(int32(spec.skinRef))
	w.i32(int32(spec.shaderRef))
	w.i32(int32(spec.alphaRef))
	w.u64(vertexDesc)
	w.u16(uint16(len(spec.triangles)))
	w.u16(uint16(len(spec.vertices)))
	w.u32(uint32(len(spec.vertices) * vertexStride))
	for _, v := range spec.vertices {
		w.vertex(v)
	}
	for _, triangle := range spec.triangles {
		w.u16(triangle[0])
		w.u16(triangle[1])
		w.u16(triangle[2])
	}
	return b.add("BSTriShape", w.out)
}

func (b *builder) skinInstance(kind string, dataRef, partitionRef int, boneRefs []int) int {
	w := &writer{}
	w.i32(int32(dataRef))
	w.i32(int32(partitionRef))
	w.i32(0) // skeleton root
	w.u32(uint32(len(boneRefs)))
	for _, ref := range boneRefs {
		w.i32(int32(ref))
	}
	return b.add(kind, w.out)
}

// skinData writes NiSkinData with identity bind transforms, so a skinned shape
// keeps the positions the fixture gave it.
func (b *builder) skinData(numBones int, vertsPerBone []int) int {
	w := &writer{}
	w.zeros(52) // overall transform
	w.u32(uint32(numBones))
	w.u8(1) // has vertex weights
	for bone := range numBones {
		w.f32s(identityRotation()...)
		w.f32s(0, 0, 0)
		w.f32(1)
		w.zeros(16) // bounding sphere
		count := 0
		if bone < len(vertsPerBone) {
			count = vertsPerBone[bone]
		}
		w.u16(uint16(count))
		w.zeros(6 * count)
	}
	return b.add("NiSkinData", w.out)
}

type partitionSpec struct {
	numVerts    int
	bonePalette []uint16
	triangles   [][3]uint16
}

func (b *builder) skinPartition(partitions []partitionSpec) int {
	w := &writer{}
	w.u32(uint32(len(partitions)))
	w.u32(0) // data size: the shape's own vertex buffer wins
	w.u32(vertexStride)
	w.u64(vertexDesc)
	for _, p := range partitions {
		w.u16(uint16(p.numVerts))
		w.u16(uint16(len(p.triangles)))
		w.u16(uint16(len(p.bonePalette)))
		w.u16(0) // strips
		w.u16(4) // weights per vertex
		for _, bone := range p.bonePalette {
			w.u16(bone)
		}
		w.u8(0) // no vertex map
		w.u8(0) // no per-partition weights
		w.u8(0) // no faces in the LE-shaped section
		w.u8(0) // no per-partition bone indices
		w.zeros(2)
		w.zeros(8) // dismember flags and body part
		for _, triangle := range p.triangles {
			w.u16(triangle[0])
			w.u16(triangle[1])
			w.u16(triangle[2])
		}
	}
	return b.add("NiSkinPartition", w.out)
}

func (b *builder) textureSet(paths ...string) int {
	w := &writer{}
	w.u32(uint32(len(paths)))
	for _, path := range paths {
		w.sizedString(path)
	}
	return b.add("BSShaderTextureSet", w.out)
}

func (b *builder) shader(name string, shaderType uint32, textureRef int, glossiness float64, specular [3]float64) int {
	w := &writer{}
	w.u32(shaderType)
	w.i32(b.stringIndex(name))
	w.u32(0) // extra data
	w.i32(-1)
	w.zeros(8)  // shader flags
	w.zeros(16) // UV offset and scale
	w.i32(int32(textureRef))
	w.f32s(0, 0, 0) // emissive colour
	w.f32(1)        // emissive multiple
	w.u32(0)        // texture clamp mode
	w.f32(1)        // alpha
	w.f32(0)        // refraction strength
	w.f32(glossiness)
	w.f32s(specular[:]...)
	return b.add("BSLightingShaderProperty", w.out)
}

func (b *builder) alpha(flags uint16, threshold uint8) int {
	w := &writer{}
	w.i32(-1)
	w.u32(0)
	w.i32(-1)
	w.u16(flags)
	w.u8(threshold)
	return b.add("NiAlphaProperty", w.out)
}
