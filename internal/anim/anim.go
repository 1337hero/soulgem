// Package anim decodes game hkaSplineCompressedAnimation tracks.
//
// The pipeline is one stage per file: hkx.go shells out to hkxc for the binary
// container, xml.go extracts the animation header and spline block from its
// XML, spline.go decodes the block into sampleable tracks, and binary.go writes
// the sampled clip.
package anim

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"soulgem/internal/mathutil"
)

type Bone struct {
	Pos   [][3]float64
	Rot   [][4]float64
	Scale [][3]float64
}

type Clip struct {
	Duration float64
	FPS      float64
	Frames   int
	Bones    map[string]Bone
	Order    []string
}

// Decode samples every track of every frame into flat per-bone arrays.
func Decode(path, root string) (*Clip, error) {
	info, err := LoadHKX(path, root)
	if err != nil {
		return nil, err
	}
	return info.Clip(path)
}

// Clip decodes an already-extracted animation. Splitting it from Decode keeps
// the sampling testable without an HKX file and an hkxc on the path.
func (info *Info) Clip(path string) (*Clip, error) {
	var err error
	blocks := make([][]track, len(info.BlockOffsets))
	for i, off := range info.BlockOffsets {
		blocks[i], err = decodeBlock(info.Data, off, info.NumTracks, info.NumFloatTracks)
		if err != nil {
			return nil, fmt.Errorf("%s: block %d: %w", path, i, err)
		}
	}
	names := info.TrackNames[:info.NumTracks]
	clip := &Clip{
		Duration: info.Duration, FPS: 1 / info.FrameDuration, Frames: info.NumFrames,
		Bones: make(map[string]Bone, len(names)), Order: append([]string(nil), names...),
	}
	// Blocks overlap by one frame: the last frame of a block is the first of
	// the next, so each block advances by maxFramesPerBlock-1.
	span := info.MaxFramesPerBlock - 1
	for trackIndex, name := range names {
		bone := Bone{
			Pos:   make([][3]float64, 0, info.NumFrames),
			Rot:   make([][4]float64, 0, info.NumFrames),
			Scale: make([][3]float64, 0, info.NumFrames),
		}
		identityScale := true
		for frame := range info.NumFrames {
			block := min(frame/span, len(blocks)-1)
			if trackIndex >= len(blocks[block]) {
				return nil, fmt.Errorf("%s: block %d decoded %d of %d tracks",
					path, block, len(blocks[block]), info.NumTracks)
			}
			current := blocks[block][trackIndex]
			local := float64(frame - block*span)
			pos := current.pos.sample(local)
			rot := current.rot.sample(local)
			scale := current.scale.sample(local)
			// Rounding keeps near-constant tracks exactly equal so the binary
			// writer can collapse them; the dropped digits are below the
			// precision the spline encoding carries.
			for i := range pos {
				pos[i] = mathutil.Round(pos[i], 4)
				scale[i] = mathutil.Round(scale[i], 4)
			}
			for i := range rot {
				rot[i] = mathutil.Round(rot[i], 6)
			}
			bone.Pos = append(bone.Pos, pos)
			bone.Rot = append(bone.Rot, rot)
			bone.Scale = append(bone.Scale, scale)
			identityScale = identityScale && scale == [3]float64{1, 1, 1}
		}
		if identityScale {
			bone.Scale = nil
		}
		clip.Bones[name] = bone
	}
	return clip, nil
}

// Reindex rewrites index.json to list every clip in the output directory.
func Reindex(outDir string) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if clip, ok := strings.CutSuffix(name, ".anim"); ok {
			names = append(names, clip)
		}
	}
	slices.Sort(names)
	data, err := json.Marshal(names)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "index.json"), data, 0o644); err != nil {
		return nil, err
	}
	return names, nil
}
