package character

import (
	"path/filepath"

	"soulgem/internal/mathutil"
)

func loadLydia(root, _, staging string) Config {
	gto := filepath.Join(staging, "Girl's Travel Outfit CBBE-125910-1-1-2-1727489948")
	hair := mathutil.Vec3{0.035, 0.025, 0.018}
	return Config{
		Name: "Lydia", Data: Data,
		DataRoots: []string{filepath.Join(root, "characters/lydia"), gto},
		Out:       filepath.Join(root, "lydia.glb"),
		Meshes: []Mesh{
			{Path: filepath.Join(gto, "Meshes/Girl's Travel Outfit/torso_1.nif")},
			{Path: filepath.Join(gto, "Meshes/Girl's Travel Outfit/gloves_1.nif")},
			{Path: filepath.Join(gto, "Meshes/Girl's Travel Outfit/boots_1.nif")},
			{Path: filepath.Join(gto, "Meshes/Girl's Travel Outfit/choker.nif")},
			{Path: "meshes/actors/character/facegendata/facegeom/skyrim.esm/000A2C8E.NIF"},
		},
		BodyMatch: "character/skin/female", BodyFactors: mathutil.Vec3{0.9372 * 1.92, 0.8667 * 1.86, 0.8667 * 1.85},
		HairTint: &hair, FaceTint: "actors/character/FaceGenData/FaceTint/Skyrim.esm/000A2C8E.dds",
		HeadShape: "LydiaHeadHP", HeadTriBSA: HeadTriBSA, HeadTri: HeadTri,
		MorphNames: append([]string(nil), MorphNames...),
		Remap: map[string]string{
			`ks hairdo's\dawn.dds`:                      "actors/character/Lydia/hair/Dawn.dds",
			`ks hairdo's\dawn_n.dds`:                    "actors/character/Lydia/hair/Dawn_n.dds",
			`ks hairdo's\hairline\long.dds`:             "actors/character/Lydia/hair/long.dds",
			`ks hairdo's\hairline\long_n.dds`:           "actors/character/Lydia/hair/long_n.dds",
			`actors\character\eyes\eyebrown.dds`:        "actors/character/Lydia/eyes/HumanEyes15.dds",
			`actors\character\eyes\eyegreen.dds`:        "actors/character/Lydia/eyes/eyegreen.dds",
			`actors\character\eyes\eyebrown_n.dds`:      "actors/character/Lydia/eyes/eyebrown_n.dds",
			`actors\character\female\femalebody_1.dds`:  "actors/character/skin/femalebody_1.dds",
			`actors\character\female\femalehands_1.dds`: "actors/character/skin/femalehands_1.dds",
			`actors\character\female\astridbody.dds`:    "actors/character/skin/femalebody_1.dds",
		},
	}
}
