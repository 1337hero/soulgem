package main

import (
	"fmt"
	"os"
	"path/filepath"

	"soulgem/internal/nif"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nif FILE...")
		os.Exit(2)
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(paths []string) error {
	for _, path := range paths {
		f, err := nif.Open(path, nil)
		if err != nil {
			return err
		}
		fmt.Printf("== %s  (stream %d, blocks: %d)\n", filepath.Base(path), f.Stream, len(f.BlockTypes))
		for _, shape := range f.Shapes {
			minZ, maxZ := 0.0, 0.0
			if len(shape.Positions) != 0 {
				minZ, maxZ = shape.Positions[0][2], shape.Positions[0][2]
				for _, p := range shape.Positions[1:] {
					minZ = min(minZ, p[2])
					maxZ = max(maxZ, p[2])
				}
			}
			fmt.Printf("  shape %q verts=%d tris=%d skinned=%s shaderType=%v alpha=%v z=[%.1f,%.1f] uv=%s col=%s\n",
				shape.Name, len(shape.Positions), len(shape.Triangles), pyBool(shape.Skinned),
				shape.ShaderType, alpha(shape.AlphaFlags), minZ, maxZ,
				pyBool(len(shape.UVs) > 0), pyBool(len(shape.Colors) > 0))
			for _, texture := range shape.Textures {
				if texture != "" {
					fmt.Printf("      tex: %s\n", texture)
				}
			}
		}
	}
	return nil
}

func alpha(v *uint16) any {
	if v == nil {
		return "None"
	}
	return *v
}

func pyBool(v bool) string {
	if v {
		return "True"
	}
	return "False"
}
