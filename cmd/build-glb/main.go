package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"soulgem/internal/character"
	"soulgem/internal/glb"
	"soulgem/internal/project"
)

func main() {
	verify := flag.Bool("verify", false, "build to a scratch file and fail if output moved")
	freeze := flag.Bool("freeze", false, "record current output hashes as baselines")
	flag.Parse()
	if err := run(flag.Args(), *verify, *freeze); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(names []string, verify, freeze bool) error {
	if len(names) == 0 {
		names = character.Names()
		slices.Sort(names)
	}
	root, err := project.Root()
	if err != nil {
		return err
	}
	assets, err := glb.NewAssets(root)
	if err != nil {
		return err
	}
	var failed []string
	for _, name := range names {
		cfg, err := character.Load(name, root)
		if err != nil {
			return err
		}
		out := cfg.Out
		if verify {
			out = filepath.Join(assets.Cache, name+".verify.glb")
		}
		data, report, err := glb.Build(assets, cfg)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		printReport(report, out)
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
		hash, err := glb.SHA(out)
		if err != nil {
			return err
		}
		hashFile := filepath.Join(root, "characters", name, name+".sha256")
		switch {
		case freeze:
			if err := os.WriteFile(hashFile, []byte(hash+"\n"), 0o644); err != nil {
				return err
			}
			fmt.Printf("froze %s\n", name)
		case verify:
			wantData, err := os.ReadFile(hashFile)
			if err != nil {
				return err
			}
			want := strings.TrimSpace(string(wantData))
			if err := os.Remove(out); err != nil {
				return err
			}
			if hash == want {
				fmt.Printf("%s: ok\n", name)
			} else {
				fmt.Printf("%s: CHANGED\n  was %s\n  now %s\n", name, want, hash)
				failed = append(failed, name)
			}
		}
	}
	if len(failed) != 0 {
		return fmt.Errorf("output changed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

func printReport(report glb.Report, out string) {
	for _, part := range report.Parts {
		if part.MorphTargets != 0 {
			fmt.Printf("  + %d morph targets on %s\n", part.MorphTargets, part.Name)
		}
		fmt.Printf("%s :: %s: %dv, tex=%s\n", part.Source, part.Name, part.Vertices, part.Texture)
	}
	fmt.Printf("\nwrote %s: %.2f MB, %d meshes, %d textures\n",
		out, float64(report.Size)/1e6, report.Meshes, report.Textures)
}
