package character

import (
	"fmt"
	"os"
	"path/filepath"

	"soulgem/internal/mathutil"
)

const Data = "/home/mikekey/.local/share/Steam/steamapps/common/Skyrim Special Edition/Data"

var (
	TextureBSAs = []string{
		"Skyrim - Textures0.bsa", "Skyrim - Textures1.bsa", "Skyrim - Textures2.bsa",
		"Skyrim - Textures3.bsa", "Skyrim - Textures4.bsa", "Skyrim - Textures5.bsa",
		"Skyrim - Textures6.bsa", "Skyrim - Textures7.bsa", "Skyrim - Textures8.bsa",
	}
	MorphNames = []string{
		"Aah", "BigAah", "BMP", "ChJSh", "DST", "Eee", "Eh", "FV", "I", "K", "N",
		"Oh", "OohQ", "R", "Th", "W", "BlinkLeft", "BlinkRight", "SquintLeft",
		"SquintRight", "LookDown", "BrowDownLeft", "BrowDownRight", "BrowInLeft",
		"BrowInRight", "BrowUpLeft", "BrowUpRight", "MoodHappy", "MoodSad",
		"MoodAnger", "MoodFear", "MoodSurprise", "MoodPuzzled", "MoodDisgusted",
	}
)

const (
	HeadTriBSA = "Skyrim - Meshes0.bsa"
	HeadTri    = "meshes/actors/character/character assets/femalehead.tri"
)

type MeshOptions struct {
	Skip map[string]bool
	Tint *mathutil.Vec3
}

type Mesh struct {
	Path    string
	Options MeshOptions
}

type Config struct {
	Name        string
	Data        string
	DataRoots   []string
	TextureBSAs []string
	Out         string
	Skeleton    string
	Meshes      []Mesh
	BodyMatch   string
	BodyFactors mathutil.Vec3
	HairTint    *mathutil.Vec3
	FaceTint    string
	HeadShape   string
	HeadTriBSA  string
	HeadTri     string
	MorphNames  []string
	Remap       map[string]string
}

type loader func(root, home, staging string) Config

var loaders = map[string]loader{
	"aster":       loadAster,
	"lydia":       loadLydia,
	"serana":      loadSerana,
	"serana_nude": loadSeranaNude,
}

func Names() []string { return []string{"aster", "lydia", "serana", "serana_nude"} }

func Load(name, root string) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	load, ok := loaders[name]
	if !ok {
		return Config{}, fmt.Errorf("unknown character %q", name)
	}
	staging := filepath.Join(home, ".config/steamtinkerlaunch/vortex/staging/skyrimse/mods")
	return load(root, home, staging), nil
}
