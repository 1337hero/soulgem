package character

import (
	"fmt"
	"os"
	"path/filepath"

	"soulgem/internal/mathutil"
)

// Data is the Skyrim SE install used by the offline asset pipeline. It lives
// on the removable CARTRIDGE Steam library, so it is often absent: commands
// that need it fail with a clear path, and tests skip.
const Data = "/run/media/mikekey/CARTRIDGE/SteamLibrary/steamapps/common/Skyrim Special Edition/Data"

var (
	TextureBSAs = []string{
		"SkyrimSE - Textures0.bsa", "SkyrimSE - Textures1.bsa", "SkyrimSE - Textures2.bsa",
		"SkyrimSE - Textures3.bsa", "SkyrimSE - Textures4.bsa", "SkyrimSE - Textures5.bsa",
		"SkyrimSE - Textures6.bsa", "SkyrimSE - Textures7.bsa", "SkyrimSE - Textures8.bsa",
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
	HeadTriBSA = "SkyrimSE - Meshes0.bsa"
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
