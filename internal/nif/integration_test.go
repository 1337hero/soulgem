package nif_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"soulgem/internal/nif"
)

func TestSkinnedSSEOutfit(t *testing.T) {
	path := filepath.Join("..", "..", "texcache", "vampirerobesf_alt_1.nif")
	file, err := nif.Open(path, nil)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("local Skyrim NIF fixture is not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	if file.Stream != 100 || len(file.BlockTypes) != 46 || len(file.Shapes) != 2 {
		t.Fatalf("unexpected NIF summary: stream=%d blocks=%d shapes=%d",
			file.Stream, len(file.BlockTypes), len(file.Shapes))
	}
	skin, robes := file.Shapes[0], file.Shapes[1]
	if skin.Name != "robes_skin_alt" || len(skin.Positions) != 88 ||
		len(skin.Triangles) != 128 || !skin.Skinned {
		t.Fatalf("unexpected skin shape: %#v", skin)
	}
	if robes.Name != "vampire_robes_alt" || len(robes.Positions) != 2568 ||
		len(robes.Triangles) != 3488 || !robes.Skinned {
		t.Fatalf("unexpected robes shape: %#v", robes)
	}
}
