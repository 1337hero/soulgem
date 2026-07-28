package gltf

import (
	"encoding/binary"
	"math"

	"soulgem/internal/mathutil"
)

// Writer accumulates a document and its binary chunk together, so nothing else
// has to know how accessors, buffer views and byte offsets line up.
//
// Buffer views are appended in call order and their offsets are baked into the
// binary chunk, so the order in which a caller adds attributes is part of the
// output. The build stage adds them in a fixed order for that reason.
type Writer struct {
	Document Document
	buffer   []byte
	pngIndex map[string]int
}

func NewWriter(name string) *Writer {
	return &Writer{
		Document: Document{
			Asset:    Asset{Version: "2.0", Generator: name},
			Samplers: []Sampler{DefaultSampler()},
		},
		pngIndex: make(map[string]int),
	}
}

// appendTo adds an item to one of the document's lists and returns its index,
// which is how every element of a glTF file refers to every other.
func appendTo[T any](list *[]T, item T) int {
	*list = append(*list, item)
	return len(*list) - 1
}

// AddView copies data into the binary chunk, four-byte aligned, and returns the
// index of the buffer view describing it.
func (w *Writer) AddView(data []byte, target int) int {
	for len(w.buffer)%4 != 0 {
		w.buffer = append(w.buffer, 0)
	}
	index := appendTo(&w.Document.BufferViews, BufferView{
		Buffer: 0, ByteOffset: len(w.buffer), ByteLength: len(data), Target: target,
	})
	w.buffer = append(w.buffer, data...)
	return index
}

// accessor writes data into the binary chunk and appends the accessor
// describing it, returning the accessor index.
func (w *Writer) accessor(data []byte, target int, a Accessor) int {
	a.BufferView = w.AddView(data, target)
	return appendTo(&w.Document.Accessors, a)
}

func floatAccessor(kind string, count int) Accessor {
	return Accessor{ComponentType: componentFloat, Count: count, Type: kind}
}

// AddPositions writes a VEC3 accessor with the min/max bounds glTF requires on
// position data.
func (w *Writer) AddPositions(values []mathutil.Vec3) int {
	a := floatAccessor("VEC3", len(values))
	a.Min, a.Max = bounds(values)
	return w.accessor(vec3Bytes(values), targetArrayBuffer, a)
}

func (w *Writer) AddVec2(values [][2]float64) int {
	data := packFloats(len(values), func(i int) []float64 { return values[i][:] })
	return w.accessor(data, targetArrayBuffer, floatAccessor("VEC2", len(values)))
}

func (w *Writer) AddVec3(values []mathutil.Vec3) int {
	return w.accessor(vec3Bytes(values), targetArrayBuffer, floatAccessor("VEC3", len(values)))
}

func (w *Writer) AddVec4(values [][4]float64) int {
	data := packFloats(len(values), func(i int) []float64 { return values[i][:] })
	return w.accessor(data, targetArrayBuffer, floatAccessor("VEC4", len(values)))
}

// AddMatrices writes a MAT4 accessor from matrices already flattened in the
// column-major order glTF wants.
func (w *Writer) AddMatrices(values [][16]float64) int {
	data := packFloats(len(values), func(i int) []float64 { return values[i][:] })
	return w.accessor(data, 0, floatAccessor("MAT4", len(values)))
}

func (w *Writer) AddJoints(values [][4]uint16) int {
	data := packU16s(len(values), func(i int) []uint16 { return values[i][:] })
	return w.accessor(data, targetArrayBuffer, Accessor{
		ComponentType: componentUnsignedShort, Count: len(values), Type: "VEC4",
	})
}

func (w *Writer) AddTriangles(values [][3]uint16) int {
	data := packU16s(len(values), func(i int) []uint16 { return values[i][:] })
	return w.accessor(data, targetElementArrayBuffer, Accessor{
		ComponentType: componentUnsignedShort, Count: 3 * len(values), Type: "SCALAR",
	})
}

func vec3Bytes(values []mathutil.Vec3) []byte {
	return packFloats(len(values), func(i int) []float64 { return values[i][:] })
}

// AddImage embeds a PNG and returns its texture index, reusing the texture when
// the same image is embedded twice.
func (w *Writer) AddImage(name string, png []byte, key string) int {
	if index, ok := w.pngIndex[key]; ok {
		return index
	}
	source := appendTo(&w.Document.Images, Image{
		BufferView: w.AddView(png, 0), MimeType: "image/png", Name: name,
	})
	index := appendTo(&w.Document.Textures, Texture{Sampler: 0, Source: source})
	w.pngIndex[key] = index
	return index
}

func (w *Writer) AddNode(node Node) int { return appendTo(&w.Document.Nodes, node) }

func (w *Writer) AddMesh(mesh Mesh) int { return appendTo(&w.Document.Meshes, mesh) }

func (w *Writer) AddMaterial(material Material) int {
	return appendTo(&w.Document.Materials, material)
}

// Bytes serialises the GLB container: a 12-byte header, the JSON chunk padded
// with spaces, then the binary chunk padded with zeros.
func (w *Writer) Bytes() ([]byte, error) {
	w.Document.Buffers = []Buffer{{ByteLength: len(w.buffer)}}
	json, err := Marshal(w.Document)
	if err != nil {
		return nil, err
	}
	for len(json)%4 != 0 {
		json = append(json, ' ')
	}
	binaryChunk := w.buffer
	for len(binaryChunk)%4 != 0 {
		binaryChunk = append(binaryChunk, 0)
	}
	const (
		magic     = 0x46546c67 // "glTF"
		jsonChunk = 0x4e4f534a // "JSON"
		binChunk  = 0x004e4942 // "BIN\0"
		version   = 2
		headerLen = 28 // 12-byte header plus two 8-byte chunk headers
	)
	total := headerLen + len(json) + len(binaryChunk)
	out := make([]byte, 0, total)
	out = appendU32(out, magic)
	out = appendU32(out, version)
	out = appendU32(out, uint32(total))
	out = appendU32(out, uint32(len(json)))
	out = appendU32(out, jsonChunk)
	out = append(out, json...)
	out = appendU32(out, uint32(len(binaryChunk)))
	out = appendU32(out, binChunk)
	return append(out, binaryChunk...), nil
}

// packFloats and packU16s write the binary chunk. Go cannot range over a type
// parameter constrained to several array widths, so rows are read through an
// indexer rather than duplicating a loop per width.
func packFloats(rows int, at func(int) []float64) []byte {
	var out []byte
	for i := range rows {
		for _, value := range at(i) {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(value)))
		}
	}
	return out
}

func packU16s(rows int, at func(int) []uint16) []byte {
	var out []byte
	for i := range rows {
		for _, value := range at(i) {
			out = binary.LittleEndian.AppendUint16(out, value)
		}
	}
	return out
}

func bounds(values []mathutil.Vec3) ([]Float, []Float) {
	if len(values) == 0 {
		return nil, nil
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		for i := range 3 {
			minimum[i] = min(minimum[i], value[i])
			maximum[i] = max(maximum[i], value[i])
		}
	}
	return []Float{Float(minimum[0]), Float(minimum[1]), Float(minimum[2])},
		[]Float{Float(maximum[0]), Float(maximum[1]), Float(maximum[2])}
}

func appendU32(out []byte, value uint32) []byte {
	return binary.LittleEndian.AppendUint32(out, value)
}
