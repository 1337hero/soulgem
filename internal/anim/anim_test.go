package anim

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// blockBuilder writes a spline block the way Havok lays one out. The real ones
// come out of Bethesda's HKX files; these fixtures reproduce the encoding so
// the decoder is pinned without shipping an asset.
type blockBuilder struct {
	out    []byte
	tracks []trackSpec
}

type trackSpec struct {
	// quantization packs the position, rotation and scale encodings into one
	// byte, exactly as the format does.
	quantization byte
	posMask      byte
	rotMask      byte
	scaleMask    byte
	writePos     func(w *specWriter)
	writeRot     func(w *specWriter)
	writeScale   func(w *specWriter)
}

type specWriter struct {
	out  *[]byte
	base int
}

func (w *specWriter) u8(v byte)    { *w.out = append(*w.out, v) }
func (w *specWriter) u16(v uint16) { *w.out = binary.LittleEndian.AppendUint16(*w.out, v) }
func (w *specWriter) f32(v float64) {
	*w.out = binary.LittleEndian.AppendUint32(*w.out, math.Float32bits(float32(v)))
}
func (w *specWriter) bytes(values ...byte) { *w.out = append(*w.out, values...) }
func (w *specWriter) align(n int) {
	for (len(*w.out)-w.base)%n != 0 {
		*w.out = append(*w.out, 0)
	}
}

func (b *blockBuilder) bytes(numFloatTracks int) []byte {
	base := 0
	for _, track := range b.tracks {
		b.out = append(b.out, track.quantization, track.posMask, track.rotMask, track.scaleMask)
	}
	b.out = append(b.out, make([]byte, numFloatTracks)...)
	w := &specWriter{out: &b.out, base: base}
	w.align(4)
	for _, track := range b.tracks {
		track.writePos(w)
		track.writeRot(w)
		w.align(4)
		track.writeScale(w)
	}
	return b.out
}

// staticVec writes a vector track whose axes are all constants.
func staticVec(values [3]float64) func(*specWriter) {
	return func(w *specWriter) {
		for _, value := range values {
			w.f32(value)
		}
	}
}

func nothing(*specWriter) {}

// linearSplineX writes an X-axis spline: degree 1, two control points, so the
// value moves linearly from minimum to maximum across the block.
func linearSplineX(minimum, maximum float64) func(*specWriter) {
	return func(w *specWriter) {
		w.u16(1)            // one interval, so two control points
		w.u8(1)             // degree
		w.bytes(0, 0, 1, 1) // knots
		w.align(4)
		w.f32(minimum)
		w.f32(maximum)
		w.bytes(0, 255) // control points, quantized to one byte each
		w.align(4)
	}
}

// identityRotation writes the mask that means "no rotation track"; the decoder
// substitutes the identity quaternion.
func identityRotation(*specWriter) {}

const (
	// posDynamicX marks the X axis of a vector track as spline-encoded.
	posDynamicX = 1 << 4
	// axesStatic marks all three axes of a vector track as constants.
	axesStatic = 0b111
)

func TestDecodeBlockStaticTrack(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		quantization: 0,
		posMask:      axesStatic,
		rotMask:      0,
		scaleMask:    axesStatic,
		writePos:     staticVec([3]float64{1, 2, 3}),
		writeRot:     identityRotation,
		writeScale:   staticVec([3]float64{1, 1, 1}),
	}}}
	tracks, err := decodeBlock(b.bytes(0), 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks", len(tracks))
	}
	if got := tracks[0].pos.sample(0); got != [3]float64{1, 2, 3} {
		t.Errorf("pos = %v", got)
	}
	if got := tracks[0].rot.sample(0); got != [4]float64{0, 0, 0, 1} {
		t.Errorf("rot = %v, want identity", got)
	}
	if got := tracks[0].scale.sample(3); got != [3]float64{1, 1, 1} {
		t.Errorf("scale = %v", got)
	}
}

func TestDecodeBlockSplineTrack(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		quantization: 0, // one-byte control points
		posMask:      posDynamicX,
		rotMask:      0,
		scaleMask:    axesStatic,
		writePos:     linearSplineX(0, 10),
		writeRot:     identityRotation,
		writeScale:   staticVec([3]float64{1, 1, 1}),
	}}}
	tracks, err := decodeBlock(b.bytes(0), 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ frame, want float64 }{{0, 0}, {0.5, 5}, {1, 10}} {
		got := tracks[0].pos.sample(test.frame)
		if math.Abs(got[0]-test.want) > 1e-9 {
			t.Errorf("pos.X at frame %v = %v, want %v", test.frame, got[0], test.want)
		}
		if got[1] != 0 || got[2] != 0 {
			t.Errorf("untracked axes should stay at the identity, got %v", got)
		}
	}
}

func TestDecodeBlockRejectsBadDegree(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		posMask: posDynamicX,
		writePos: func(w *specWriter) {
			w.u16(1)
			w.u8(200) // no spline is degree 200
			w.bytes(make([]byte, 64)...)
		},
		writeRot:   identityRotation,
		writeScale: nothing,
	}}}
	_, err := decodeBlock(b.bytes(0), 0, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "degree") {
		t.Fatalf("err = %v, want a degree complaint rather than a panic", err)
	}
}

func TestDecodeBlockRejectsTruncatedData(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		posMask: axesStatic, scaleMask: axesStatic,
		writePos:   staticVec([3]float64{1, 2, 3}),
		writeRot:   identityRotation,
		writeScale: staticVec([3]float64{1, 1, 1}),
	}}}
	full := b.bytes(0)
	for cut := range len(full) {
		if _, err := decodeBlock(full[:cut], 0, 1, 0); err == nil {
			t.Fatalf("truncating to %d of %d bytes decoded successfully", cut, len(full))
		}
	}
}

func TestDecodeBlockRejectsUnsupportedRotationQuantization(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		quantization: 0, // rotQuant becomes 2, which nothing decodes
		rotMask:      0b1,
		writePos:     nothing,
		writeRot:     func(w *specWriter) { w.bytes(make([]byte, 16)...) },
		writeScale:   nothing,
	}}}
	_, err := decodeBlock(b.bytes(0), 0, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "quantization") {
		t.Fatalf("err = %v", err)
	}
}

// splineXML wraps a block in the XML shape hkxc produces.
func splineXML(data []byte, numFrames, numTracks, maxFramesPerBlock int, trackNames []string) []byte {
	numbers := make([]string, len(data))
	for i, b := range data {
		numbers[i] = fmt.Sprint(int(b))
	}
	names := ""
	for _, name := range trackNames {
		names += fmt.Sprintf(`<hkobject><hkparam name="trackName">%s</hkparam></hkobject>`, name)
	}
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="ascii"?>
<hkpackfile><hksection name="__data__">
<hkobject name="#0001" class="hkaSplineCompressedAnimation" signature="0x792ee0bb">
  <hkparam name="duration">1.5</hkparam>
  <hkparam name="frameDuration">0.0333333</hkparam>
  <hkparam name="numFrames">%d</hkparam>
  <hkparam name="numberOfTransformTracks">%d</hkparam>
  <hkparam name="numberOfFloatTracks">0</hkparam>
  <hkparam name="numBlocks">1</hkparam>
  <hkparam name="maxFramesPerBlock">%d</hkparam>
  <hkparam name="blockOffsets" numelements="1">0</hkparam>
  <hkparam name="data" numelements="%d">%s</hkparam>
  <hkparam name="annotationTracks">%s</hkparam>
</hkobject>
</hksection></hkpackfile>`, numFrames, numTracks, maxFramesPerBlock,
		len(data), strings.Join(numbers, " "), names)
}

func TestParseXML(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		posMask: axesStatic, scaleMask: axesStatic,
		writePos:   staticVec([3]float64{1, 2, 3}),
		writeRot:   identityRotation,
		writeScale: staticVec([3]float64{1, 1, 1}),
	}}}
	info, err := parseXML(splineXML(b.bytes(0), 2, 1, 2, []string{"NPC Root [Root]"}))
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 1.5 || info.NumFrames != 2 || info.NumTracks != 1 {
		t.Errorf("info = %+v", info)
	}
	if len(info.TrackNames) != 1 || info.TrackNames[0] != "NPC Root [Root]" {
		t.Errorf("TrackNames = %v", info.TrackNames)
	}
	if len(info.BlockOffsets) != 1 || info.BlockOffsets[0] != 0 {
		t.Errorf("BlockOffsets = %v", info.BlockOffsets)
	}
}

func TestParseXMLRejectsBadDocuments(t *testing.T) {
	cases := map[string]string{
		"not XML":            "{}",
		"no animation":       `<hkpackfile><hkobject class="hkaSkeleton"/></hkpackfile>`,
		"missing a field":    `<hkobject class="hkaSplineCompressedAnimation"><hkparam name="duration">1.0</hkparam></hkobject>`,
		"unparseable number": `<hkobject class="hkaSplineCompressedAnimation"><hkparam name="duration">soon</hkparam></hkobject>`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseXML([]byte(document)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseXMLRejectsImpossibleHeaders(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		posMask: axesStatic, scaleMask: axesStatic,
		writePos:   staticVec([3]float64{1, 2, 3}),
		writeRot:   identityRotation,
		writeScale: staticVec([3]float64{1, 1, 1}),
	}}}
	data := b.bytes(0)
	// maxFramesPerBlock of 1 would make the per-block frame span zero and
	// divide by zero while sampling.
	if _, err := parseXML(splineXML(data, 2, 1, 1, []string{"Root"})); err == nil {
		t.Error("maxFramesPerBlock of 1 should be rejected")
	}
	if _, err := parseXML(splineXML(data, 0, 1, 2, []string{"Root"})); err == nil {
		t.Error("a clip with no frames should be rejected")
	}
}

func TestClipSamplesAndSerialises(t *testing.T) {
	b := &blockBuilder{tracks: []trackSpec{{
		posMask:    posDynamicX,
		scaleMask:  axesStatic,
		writePos:   linearSplineX(0, 10),
		writeRot:   identityRotation,
		writeScale: staticVec([3]float64{1, 1, 1}),
	}}}
	info, err := parseXML(splineXML(b.bytes(0), 2, 1, 2, []string{"NPC Root [Root]"}))
	if err != nil {
		t.Fatal(err)
	}
	clip, err := info.Clip("test.hkx")
	if err != nil {
		t.Fatal(err)
	}
	bone := clip.Bones["NPC Root [Root]"]
	if len(bone.Pos) != 2 || bone.Pos[0] != [3]float64{0, 0, 0} || bone.Pos[1] != [3]float64{10, 0, 0} {
		t.Fatalf("Pos = %v", bone.Pos)
	}
	// An all-identity scale track is dropped rather than written out per frame.
	if bone.Scale != nil {
		t.Errorf("Scale = %v, want it omitted", bone.Scale)
	}
	data, err := clip.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	header, payload := splitBinary(t, data)
	want := BinaryHeader{Duration: 1.5, FPS: clip.FPS, Frames: 2, Tracks: []BinaryTrack{
		// moving position keeps both frames; the identity rotation collapses to one
		{Bone: "NPC Root [Root]", Pos: 0, PosStride: 3, Rot: 6, RotStride: 0},
	}}
	if !reflect.DeepEqual(header, want) {
		t.Errorf("header = %+v, want %+v", header, want)
	}
	if wantPayload := []float32{0, 0, 0, 10, 0, 0, 0, 0, 0, 1}; !slices.Equal(payload, wantPayload) {
		t.Errorf("payload = %v, want %v", payload, wantPayload)
	}
}

// splitBinary decodes an .anim file the way the viewer does.
func splitBinary(t *testing.T, data []byte) (BinaryHeader, []float32) {
	t.Helper()
	if string(data[:4]) != BinaryMagic {
		t.Fatalf("magic = %q", data[:4])
	}
	size := int(binary.LittleEndian.Uint32(data[4:8]))
	if (8+size)%4 != 0 {
		t.Fatalf("payload starts at %d, not 4-byte aligned", 8+size)
	}
	var header BinaryHeader
	if err := json.Unmarshal(data[8:8+size], &header); err != nil {
		t.Fatal(err)
	}
	var payload []float32
	for offset := 8 + size; offset < len(data); offset += 4 {
		payload = append(payload, math.Float32frombits(binary.LittleEndian.Uint32(data[offset:])))
	}
	return header, payload
}
