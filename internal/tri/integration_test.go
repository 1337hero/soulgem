package tri_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"soulgem/internal/bsa"
	"soulgem/internal/tri"
)

func TestVanillaFemaleHead(t *testing.T) {
	const gameData = "/home/mikekey/.local/share/Steam/steamapps/common/SkyrimSE/Data"
	path := filepath.Join(gameData, "SkyrimSE - Meshes0.bsa")
	archive, err := bsa.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("Game assets are not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := archive.Read("meshes/actors/character/character assets/femalehead.tri")
	if err != nil {
		t.Fatal(err)
	}
	file, err := tri.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.NumVerts != 996 || len(file.Morphs) != 44 || file.Trailing != 0 {
		t.Fatalf("femalehead.tri = %d vertices, %d morphs, %d trailing",
			file.NumVerts, len(file.Morphs), file.Trailing)
	}
	if len(file.Morphs["Aah"]) != 996 || len(file.Morphs["MoodHappy"]) != 996 {
		t.Fatal("expected head morphs are missing")
	}
}
