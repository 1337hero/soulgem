package main

import (
	"fmt"
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
	for _, path := range paths {
		if path == "--reindex" {
			continue
		}
		if err := decode(path, root, outDir); err != nil {
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

func decode(path, root, outDir string) error {
	name := strings.ToLower(hkxSuffix.ReplaceAllString(filepath.Base(path), ""))
	clip, err := anim.Decode(path, root)
	if err != nil {
		return err
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

func pyList(values []string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = "'" + value + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
