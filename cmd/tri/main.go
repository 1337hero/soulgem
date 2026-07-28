package main

import (
	"fmt"
	"math"
	"os"
	"strings"

	"soulgem/internal/bsa"
	"soulgem/internal/tri"
)

func main() {
	args := os.Args[1:]
	fromArchive := len(args) != 0 && strings.HasSuffix(strings.ToLower(args[0]), ".bsa")
	if len(args) == 0 || (fromArchive && len(args) != 2) {
		fmt.Fprintln(os.Stderr, "usage: tri FILE | tri ARCHIVE PATH")
		os.Exit(2)
	}
	if err := run(args, fromArchive); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, fromArchive bool) error {
	data, err := read(args, fromArchive)
	if err != nil {
		return err
	}
	t, err := tri.Parse(data)
	if err != nil {
		return err
	}
	fmt.Printf("verts=%d morphs=%d trailing_bytes=%d\n", t.NumVerts, len(t.Morphs), t.Trailing)
	for _, name := range t.Order {
		var maxDelta float64
		for _, v := range t.Morphs[name] {
			for _, c := range v {
				maxDelta = math.Max(maxDelta, math.Abs(c))
			}
		}
		fmt.Printf("  %s: max_delta=%.3f\n", name, maxDelta)
	}
	return nil
}

func read(args []string, fromArchive bool) ([]byte, error) {
	if !fromArchive {
		return os.ReadFile(args[0])
	}
	archive, err := bsa.Open(args[0])
	if err != nil {
		return nil, err
	}
	return archive.Read(args[1])
}
