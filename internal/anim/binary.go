package anim

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"slices"
)

// BinaryMagic opens every .anim file the viewer loads.
const BinaryMagic = "SGA1"

// BinaryHeader is the JSON block between the magic and the float payload.
// Offsets count float32s from the start of the payload. A track whose samples
// never change stores one sample with stride 0, so the viewer indexes every
// track as offset + frame*stride without branching on constant tracks.
type BinaryHeader struct {
	Duration float64       `json:"duration"`
	FPS      float64       `json:"fps"`
	Frames   int           `json:"frames"`
	Tracks   []BinaryTrack `json:"tracks"`
}

type BinaryTrack struct {
	Bone      string `json:"bone"`
	Pos       int    `json:"pos"`
	PosStride int    `json:"posStride"`
	Rot       int    `json:"rot"`
	RotStride int    `json:"rotStride"`
}

// MarshalBinary writes the clip as the viewer's .anim format:
//
//	"SGA1" | u32 header length | JSON header (space-padded to 4 bytes) | float32 LE payload
//
// Scale tracks are left out; the viewer never applied them.
func (c *Clip) MarshalBinary() ([]byte, error) {
	header := BinaryHeader{Duration: c.Duration, FPS: c.FPS, Frames: c.Frames}
	var payload []float32
	for _, name := range c.Order {
		bone := c.Bones[name]
		track := BinaryTrack{Bone: name, Pos: len(payload)}
		payload, track.PosStride = appendTrack(payload, len(bone.Pos), func(i int) []float64 { return bone.Pos[i][:] })
		track.Rot = len(payload)
		payload, track.RotStride = appendTrack(payload, len(bone.Rot), func(i int) []float64 { return bone.Rot[i][:] })
		header.Tracks = append(header.Tracks, track)
	}
	text, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	for (len(BinaryMagic)+4+len(text))%4 != 0 {
		text = append(text, ' ')
	}
	out := make([]byte, 0, len(BinaryMagic)+4+len(text)+4*len(payload))
	out = append(out, BinaryMagic...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(text)))
	out = append(out, text...)
	for _, value := range payload {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(value))
	}
	return out, nil
}

// appendTrack writes every frame of a track, or just the first when all
// frames are equal, and returns the stride the viewer should step by.
func appendTrack(payload []float32, count int, at func(int) []float64) ([]float32, int) {
	if count == 0 {
		return payload, 0
	}
	width := len(at(0))
	stride := 0
	for i := 1; i < count && stride == 0; i++ {
		if !slices.Equal(at(i), at(0)) {
			stride = width
		}
	}
	if stride == 0 {
		count = 1
	}
	for i := range count {
		for _, value := range at(i) {
			payload = append(payload, float32(value))
		}
	}
	return payload, stride
}
