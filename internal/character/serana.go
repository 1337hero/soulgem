package character

import (
	"path/filepath"

	"soulgem/internal/mathutil"
)

func loadSerana(root, home, staging string) Config {
	seranaholic := filepath.Join(home, "Downloads/Seranaholic SSE 1.8-13027-1-8-5-1642185840",
		"Seranaholic SSE 1.8-FIXED/Seranaholic 1.8.4")
	options := []string{"01 Option/Eye color/Red eye", "01 Option/CBBE", "00 Main"}
	tpa := filepath.Join(staging, "Twilight Princess Armor Mashup-71182-5-2-1719932881")
	armor := filepath.Join(tpa, "meshes/Twilight Princess Armor/F")
	xpmsse := filepath.Join(staging, "XP32 Maximum Skeleton Special Extended-1988-5-06-1707663131")
	roots := []string{filepath.Join(root, "characters/serana")}
	for _, option := range options {
		roots = append(roots, filepath.Join(seranaholic, option))
	}
	roots = append(roots, tpa)
	hair := mathutil.Vec3{0.008, 0.008, 0.012}
	head := filepath.Join(seranaholic, options[0],
		"meshes/actors/character/FaceGenData/FaceGeom/Dawnguard.esm/00002b6c.nif")
	return Config{
		Name: "Serana", Data: Data, DataRoots: roots,
		TextureBSAs: append([]string(nil), TextureBSAs...),
		Out:         filepath.Join(root, "serana.glb"),
		Skeleton: filepath.Join(xpmsse,
			"meshes/actors/character/character assets female/skeleton_female.nif"),
		Meshes: []Mesh{
			{Path: filepath.Join(armor, "TwilightPrincess_Cuirass_City_1.nif")},
			{Path: filepath.Join(armor, "TwilightPrincess_Gloves_1.nif"), Options: MeshOptions{Skip: map[string]bool{"Gauntlet": true}}},
			{Path: filepath.Join(armor, "TwilightPrincess_Boots_1.nif")},
			{Path: head},
		},
		BodyMatch: "character/skin/female", BodyFactors: mathutil.Vec3{1.42, 1.42, 1.50},
		HairTint: &hair, FaceTint: "actors/character/FaceGenData/FaceTint/Dawnguard.esm/00002B6C.dds",
		HeadShape: "SeranaHeadHP", HeadTriBSA: HeadTriBSA, HeadTri: HeadTri,
		MorphNames: append([]string(nil), MorphNames...),
		Remap: map[string]string{
			`actors\character\female\femalebody_1.dds`:  "actors/character/skin/femalebody_1.dds",
			`actors\character\female\femalehands_1.dds`: "actors/character/skin/femalehands_1.dds",
			`actors\character\serana\femalehead.dds`:    "actors/character/head/femalehead.dds",
		},
	}
}
