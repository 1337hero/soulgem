package anim

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Info is one hkaSplineCompressedAnimation, as extracted from the XML hkxc
// produces: the header fields plus the raw spline block.
type Info struct {
	Duration          float64
	NumFrames         int
	NumTracks         int
	NumFloatTracks    int
	NumBlocks         int
	MaxFramesPerBlock int
	FrameDuration     float64
	BlockOffsets      []int
	Data              []byte
	TrackNames        []string
}

// LoadHKX converts an HKX to XML with hkxc and extracts the animation from it.
//
// The clips name their tracks only in the skeleton, not in each animation,
// so a clip with blank track names is matched positionally against the shared
// track order recorded under anims/.
func LoadHKX(path, root string) (*Info, error) {
	data, err := convert(path)
	if err != nil {
		return nil, err
	}
	info, err := parseXML(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	named := false
	for _, name := range info.TrackNames {
		named = named || name != ""
	}
	if !named {
		order, err := trackOrder(root)
		if err != nil {
			return nil, err
		}
		if len(order) < info.NumTracks {
			return nil, fmt.Errorf(
				"%s: animation has %d tracks, skeleton_track_order.json lists %d",
				path, info.NumTracks, len(order))
		}
		info.TrackNames = order[:info.NumTracks:info.NumTracks]
	}
	if len(info.TrackNames) < info.NumTracks {
		return nil, fmt.Errorf("%s: animation declares %d tracks but names %d",
			path, info.NumTracks, len(info.TrackNames))
	}
	return info, nil
}

// convert runs hkxc, which is the only thing that reads the binary HKX
// container. Everything downstream works from its XML.
func convert(path string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "soulgem-hkx-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	xmlPath := filepath.Join(tmp, "a.xml")
	hkxc, err := exec.LookPath("hkxc")
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return nil, err
		}
		hkxc = filepath.Join(home, ".cargo", "bin", "hkxc")
	}
	cmd := exec.Command(hkxc, "convert", "-i", path, "-o", xmlPath, "-v", "xml")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("hkxc: %w: %s", err, out)
	}
	return os.ReadFile(xmlPath)
}

func trackOrder(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "anims", "skeleton_track_order.json"))
	if err != nil {
		return nil, err
	}
	var order []string
	if err := json.Unmarshal(data, &order); err != nil {
		return nil, fmt.Errorf("skeleton_track_order.json: %w", err)
	}
	return order, nil
}
