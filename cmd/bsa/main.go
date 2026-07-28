package main

import (
	"fmt"
	"os"
	"strings"

	"soulgem/internal/bsa"
)

const usage = "usage: bsa ARCHIVE [PATTERN] | bsa extract ARCHIVE PATH OUT"

func main() {
	args := os.Args[1:]
	extract := len(args) != 0 && args[0] == "extract"
	if len(args) == 0 || (extract && len(args) != 4) {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	if extract {
		err = extractFile(args[1], args[2], args[3])
	} else {
		err = list(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func extractFile(archivePath, path, out string) error {
	archive, err := bsa.Open(archivePath)
	if err != nil {
		return err
	}
	data, err := archive.Read(path)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o644)
}

func list(args []string) error {
	archive, err := bsa.Open(args[0])
	if err != nil {
		return err
	}
	pattern := ""
	if len(args) > 1 {
		pattern = strings.ToLower(args[1])
	}
	for _, path := range archive.Order {
		if pattern == "" || strings.Contains(path, pattern) {
			fmt.Println(path)
		}
	}
	return nil
}
