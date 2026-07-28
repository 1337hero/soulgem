package character

import "path/filepath"

func loadSeranaNude(root, home, staging string) Config {
	cfg := loadSerana(root, home, staging)
	seranaholic := filepath.Join(home, "Downloads/Seranaholic SSE 1.8-13027-1-8-5-1642185840",
		"Seranaholic SSE 1.8-FIXED/Seranaholic 1.8.4")
	body := filepath.Join(seranaholic, "01 Option/CBBE/meshes/actors/character/Serana")
	head := cfg.Meshes[len(cfg.Meshes)-1]
	cfg.Out = filepath.Join(root, "characters/serana_nude/serana_nude.glb")
	cfg.Meshes = []Mesh{
		{Path: filepath.Join(body, "femalebody_1.nif")},
		{Path: filepath.Join(body, "femalehands_1.nif")},
		{Path: filepath.Join(body, "femalefeet_1.nif")},
		head,
	}
	return cfg
}
