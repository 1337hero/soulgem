package character

import (
	"path/filepath"

	"soulgem/internal/mathutil"
)

func loadAster(root, _, staging string) Config {
	bijin := filepath.Join(staging, "Bijin Warmaidens SE v3.1.3-1825-3-1-3")
	eilhart := filepath.Join(staging, "Eilhart Dress - SSE CBBE BodySlide-22270-1-0-1546282298")
	cbbe := filepath.Join(staging, "Caliente's Beautiful Bodies Enhancer CBBE - v2.0.2-198-2-0-2-1698759611")
	return Config{
		Name: "Aster", Data: Data,
		DataRoots:   []string{filepath.Join(root, "characters/aster"), bijin, eilhart, cbbe},
		TextureBSAs: append([]string(nil), TextureBSAs...),
		Out:         filepath.Join(root, "aster.glb"),
		Meshes: []Mesh{
			{Path: filepath.Join(eilhart, "meshes/NS/Eilhart/PE_1.nif")},
			{Path: filepath.Join(cbbe, "meshes/actors/character/character assets/femalehands_1.nif")},
			{Path: filepath.Join(bijin, "meshes/actors/character/FaceGenData/FaceGeom/skyrim.esm/000A2C8F.NIF")},
		},
		BodyMatch: "character/skin/female", BodyFactors: mathutil.Vec3{0.9372 * 1.92, 0.8667 * 1.86, 0.8667 * 1.85},
		FaceTint:  "actors/character/FaceGenData/FaceTint/skyrim.esm/000A2C8F.dds",
		HeadShape: "JordisHeadHP", HeadTriBSA: HeadTriBSA, HeadTri: HeadTri,
		MorphNames: append([]string(nil), MorphNames...),
		Remap: map[string]string{
			`actors\character\female\femalebody_1.dds`:  "actors/character/skin/femalebody_1.dds",
			`actors\character\female\femalehands_1.dds`: "actors/character/skin/femalehands_1.dds",
			`actors\character\jordis\femalehead.dds`:    "actors/character/head/femalehead.dds",
		},
	}
}
