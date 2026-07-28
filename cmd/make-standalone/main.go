package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"soulgem/internal/project"
)

// inlined are the clips the standalone page ships with; the rest are fetched at
// runtime and have nowhere to be fetched from here.
var inlined = []string{"pfi18", "pfi19ex"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := project.Root()
	if err != nil {
		return err
	}
	html, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		return err
	}
	bundleData, err := os.ReadFile(filepath.Join(root, "bundle.js"))
	if err != nil {
		return err
	}
	glb, err := os.ReadFile(filepath.Join(root, "lydia.glb"))
	if err != nil {
		return err
	}
	animations, err := readAnimations(root)
	if err != nil {
		return err
	}

	bundle := strings.ReplaceAll(string(bundleData), `"body.glb"`, "window.LYDIA_GLB")
	bundle = strings.ReplaceAll(bundle, `'body.glb'`, "window.LYDIA_GLB")
	if !strings.Contains(bundle, "window.LYDIA_GLB") {
		return fmt.Errorf("bundle.js does not load body.glb; the standalone page would render nothing")
	}
	inline := `<script>window.LYDIA_GLB="data:application/octet-stream;base64,` +
		base64.StdEncoding.EncodeToString(glb) + `";window.LYDIA_ANIMS={` +
		strings.Join(animations, ",") + `}</script>` + "\n" +
		`<script type="module">` + bundle + `</script>`
	page := strings.Replace(string(html),
		`<script type="module" src="bundle.js"></script>`, inline, 1)

	out := filepath.Join(root, "lydia.html")
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %.1f MB\n", out, float64(len(page))/1e6)
	return nil
}

// readAnimations returns the inlined clips as JSON object entries, skipping any
// that have not been decoded yet.
func readAnimations(root string) ([]string, error) {
	var out []string
	for _, name := range inlined {
		data, err := os.ReadFile(filepath.Join(root, "anims", name+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%q:%s", name, data))
	}
	return out, nil
}
