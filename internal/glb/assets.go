package glb

import (
	"crypto/md5"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"soulgem/internal/bsa"
	"soulgem/internal/character"
	"soulgem/internal/mathutil"
	"soulgem/internal/pyjson"
)

// Assets resolves the game's texture paths to PNG files on disk.
//
// Game textures live in three places at once — loose files under a mod's
// directory, loose files under the game's Data, and inside BSA archives — and
// the paths inside a NIF are Windows-cased. Everything to do with finding them
// and converting DDS to PNG lives here; nothing downstream touches the disk.
type Assets struct {
	Cache    string
	archives map[string]*bsa.Archive
}

func NewAssets(root string) (*Assets, error) {
	cache := filepath.Join(root, "texcache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return nil, err
	}
	return &Assets{Cache: cache, archives: make(map[string]*bsa.Archive)}, nil
}

func (a *Assets) Archive(data, name string) (*bsa.Archive, error) {
	if archive, ok := a.archives[name]; ok {
		return archive, nil
	}
	archive, err := bsa.Open(filepath.Join(data, name))
	if err != nil {
		return nil, err
	}
	a.archives[name] = archive
	return archive, nil
}

// ciFind walks a relative path case-insensitively. Game assets reference each
// other with Windows casing, which does not survive on a case-sensitive
// filesystem.
func ciFind(root, relative string) string {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	current := root
	for _, part := range strings.Split(relative, "/") {
		info, err := os.Stat(current)
		if err != nil || !info.IsDir() {
			return ""
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return ""
		}
		match := ""
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), part) {
				match = entry.Name()
				break
			}
		}
		if match == "" {
			return ""
		}
		current = filepath.Join(current, match)
	}
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		return current
	}
	return ""
}

// fromBSA extracts a texture from the first archive that has it, caching the
// extracted file.
func (a *Assets) fromBSA(data, relative string, archives []string) (string, error) {
	for _, name := range archives {
		archive, err := a.Archive(data, name)
		if err != nil {
			return "", err
		}
		if _, ok := archive.Files[strings.ToLower(relative)]; !ok {
			continue
		}
		out := filepath.Join(a.Cache, filepath.Base(relative))
		if _, err := os.Stat(out); os.IsNotExist(err) {
			extracted, err := archive.Read(relative)
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(out, extracted, 0o644); err != nil {
				return "", err
			}
		}
		return out, nil
	}
	return "", nil
}

// FindTexture resolves a NIF texture path, honouring the character's remaps and
// searching loose directories before the BSAs.
func (a *Assets) FindTexture(cfg character.Config, gamePath string) (string, error) {
	key := strings.ToLower(gamePath)
	key = strings.TrimPrefix(key, `data\`)
	key = strings.Replace(key, `textures\`, "", 1)
	relative, ok := cfg.Remap[key]
	if !ok {
		relative = strings.ReplaceAll(key, `\`, "/")
	}
	roots := append(append([]string(nil), cfg.DataRoots...), cfg.Data)
	for _, root := range roots {
		if hit := ciFind(root, "textures/"+relative); hit != "" {
			return hit, nil
		}
	}
	return a.fromBSA(cfg.Data, "textures/"+relative, cfg.TextureBSAs)
}

// tupleKey renders a vector the way Python's repr of a tuple did. It only feeds
// a cache-key hash, but the hash appears in cached filenames.
func tupleKey(values mathutil.Vec3) string {
	out := []byte{'('}
	for i, value := range values {
		if i != 0 {
			out = append(out, ", "...)
		}
		out = pyjson.AppendFloat(out, value)
	}
	return string(append(out, ')'))
}

// ToPNG converts a DDS to a cached PNG. Two conversions are special: the face
// gets its FaceGen tint multiplied in, and the body gets per-channel factors
// that match the head's skin tone.
func (a *Assets) ToPNG(cfg character.Config, ddsPath string, maxSize int) (string, error) {
	if strings.Contains(strings.ToLower(ddsPath), "femalehead") {
		return a.faceToPNG(cfg, ddsPath)
	}
	baked := strings.Contains(strings.ToLower(ddsPath), cfg.BodyMatch)
	if baked {
		maxSize = 2048
	}
	key := ddsPath
	if baked {
		key += tupleKey(cfg.BodyFactors)
	}
	key += strconv.Itoa(maxSize)
	stem := strings.TrimSuffix(filepath.Base(ddsPath), filepath.Ext(ddsPath))
	tag := fmt.Sprintf("%x", md5.Sum([]byte(key)))[:8]
	out := filepath.Join(a.Cache, stem+"."+tag+".png")
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		return out, err
	}
	args := []string{ddsPath, "-resize", fmt.Sprintf("%dx%d>", maxSize, maxSize)}
	if baked {
		for i, channel := range []string{"R", "G", "B"} {
			args = append(args, "-channel", channel, "-evaluate", "multiply",
				fmt.Sprintf("%.4f", cfg.BodyFactors[i]))
		}
		args = append(args, "+channel")
	}
	args = append(args, "-strip", "PNG:"+out)
	if err := magick("texture", args...); err != nil {
		return "", err
	}
	return out, nil
}

func (a *Assets) faceToPNG(cfg character.Config, ddsPath string) (string, error) {
	out := filepath.Join(a.Cache, cfg.HeadShape+".png")
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		return out, err
	}
	faceTint, err := a.FindTexture(cfg, cfg.FaceTint)
	if err != nil {
		return "", err
	}
	err = magick("face", ddsPath, "-resize", "2048x2048",
		"(", faceTint, "-alpha", "off", "-resize", "2048x2048", ")",
		"-compose", "multiply", "-composite", "-evaluate", "multiply", "2",
		"-strip", "PNG:"+out)
	if err != nil {
		return "", err
	}
	return out, nil
}

func magick(what string, args ...string) error {
	output, err := exec.Command("magick", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("magick %s: %w: %s", what, err, output)
	}
	return nil
}
