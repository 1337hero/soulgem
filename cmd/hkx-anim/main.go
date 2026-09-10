package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"soulgem/internal/anim"
	"soulgem/internal/project"
)

var hkxSuffix = regexp.MustCompile(`(?i)\.hkx$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(paths []string) error {
	root, err := project.Root()
	if err != nil {
		return err
	}
	outDir := filepath.Join(root, "anims")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// --loop trims each clip to one seamless loop — for full-song routines
	// (the Dance For Me dances run the length of their track) that would
	// otherwise bake tens of MB of JSON the browser loads up front.
	loop := false
	var clips []string
	for _, path := range paths {
		switch path {
		case "--reindex":
		case "--loop":
			loop = true
		default:
			clips = append(clips, path)
		}
	}
	for _, path := range clips {
		if err := decode(path, root, outDir, loop); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
	}
	names, err := anim.Reindex(outDir)
	if err != nil {
		return err
	}
	fmt.Println("index.json:", pyList(names))
	return nil
}

func decode(path, root, outDir string, loop bool) error {
	name := strings.ToLower(hkxSuffix.ReplaceAllString(filepath.Base(path), ""))
	clip, err := anim.Decode(path, root)
	if err != nil {
		return err
	}
	if loop {
		loopTrim(clip, 6, 20)
	}
	dst := filepath.Join(outDir, name+".json")
	if err := os.WriteFile(dst, clip.MarshalPythonJSON(), 0o644); err != nil {
		return err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return err
	}
	// The pelvis at frame 0 is the quickest eyeball check that a clip decoded
	// into the right space rather than plausible nonsense.
	pelvis, scaled := "", 0
	for _, boneName := range clip.Order {
		if pelvis == "" && strings.Contains(boneName, "Pelvis") {
			pelvis = boneName
		}
		if clip.Bones[boneName].Scale != nil {
			scaled++
		}
	}
	pelvisAtZero := "?"
	if pelvis != "" {
		pelvisAtZero = fmt.Sprint(clip.Bones[pelvis].Pos[0])
	}
	fmt.Printf("%s: %df %vs %d tracks (%d scaled) -> %s (%dKB)  pelvis@0=%s\n",
		name, clip.Frames, clip.Duration, len(clip.Bones), scaled, dst, info.Size()/1024, pelvisAtZero)
	return nil
}

// loopTrim shortens clip to a single seamless loop. Within [minSec, maxSec] it
// picks the end frame whose major-bone rotation is closest to frame 0, cuts
// there, then warps the last few frames onto frame 0 so the viewer's modulo
// wrap from end to start is invisible even when no exact loop point exists.
// No-op if the clip is already shorter than the window.
func loopTrim(clip *anim.Clip, minSec, maxSec float64) {
	lo, hi := int(minSec*clip.FPS), int(maxSec*clip.FPS)
	if hi >= clip.Frames {
		hi = clip.Frames - 1
	}
	if lo < 1 || lo >= hi {
		return
	}
	// Score only the bones the eye tracks (spine, limbs, head) by rotation:
	// finger/toe/face noise and the COM's large positional numbers otherwise
	// swamp the spine pop that actually reads as a glitch.
	best, bestCost := lo, math.Inf(1)
	for f := lo; f <= hi; f++ {
		cost := 0.0
		for name, b := range clip.Bones {
			if !majorBone(name) {
				continue
			}
			for i := 0; i < 4; i++ {
				d := b.Rot[f][i] - b.Rot[0][i]
				cost += d * d
			}
		}
		if cost < bestCost {
			best, bestCost = f, cost
		}
	}
	blend := min(6, best/4)
	for name, b := range clip.Bones {
		b.Pos, b.Rot = b.Pos[:best], b.Rot[:best]
		if b.Scale != nil {
			b.Scale = b.Scale[:best]
		}
		// ease the tail onto frame 0: last frame lands exactly on the start
		// pose, the few before it ramp in, so the wrap carries no jump
		for j := best - blend; j < best; j++ {
			w := float64(j-(best-blend)+1) / float64(blend)
			b.Pos[j] = lerp3(b.Pos[j], b.Pos[0], w)
			b.Rot[j] = nlerp4(b.Rot[j], b.Rot[0], w)
		}
		clip.Bones[name] = b
	}
	clip.Frames = best
	clip.Duration = float64(best) / clip.FPS
}

// majorBone excludes the small/helper bones (fingers, toes, face, twist and
// cloth helpers) whose motion hides the loop-seam pop rather than revealing it.
func majorBone(name string) bool {
	for _, skip := range []string{"Finger", "Toe", "Eye", "Brow", "Jaw",
		"Tongue", "Breast", "Hair", "Skirt", "Wing", "Tail", "Weapon",
		"Magic", "Twist", "Camera", "Look", "Translate", "Rotate"} {
		if strings.Contains(name, skip) {
			return false
		}
	}
	return true
}

func lerp3(a, b [3]float64, w float64) [3]float64 {
	return [3]float64{a[0] + (b[0]-a[0])*w, a[1] + (b[1]-a[1])*w, a[2] + (b[2]-a[2])*w}
}

// nlerp4 is a normalized quaternion lerp — exact enough for the tiny residual
// gaps a good loop point leaves, without a full slerp.
func nlerp4(a, b [4]float64, w float64) [4]float64 {
	if a[0]*b[0]+a[1]*b[1]+a[2]*b[2]+a[3]*b[3] < 0 {
		b = [4]float64{-b[0], -b[1], -b[2], -b[3]}
	}
	q := [4]float64{a[0] + (b[0]-a[0])*w, a[1] + (b[1]-a[1])*w, a[2] + (b[2]-a[2])*w, a[3] + (b[3]-a[3])*w}
	n := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	if n == 0 {
		return a
	}
	return [4]float64{q[0] / n, q[1] / n, q[2] / n, q[3] / n}
}

func pyList(values []string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = "'" + value + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
