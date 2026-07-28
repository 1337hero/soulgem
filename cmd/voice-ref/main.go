package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"soulgem/internal/bsa"
)

const gameData = "/home/mikekey/.local/share/Steam/steamapps/common/Skyrim Special Edition/Data"

var archives = []string{
	"UHDAP - en0.bsa", "UHDAP - en1.bsa", "UHDAP - en2.bsa", "UHDAP - en3.bsa",
	"UHDAP - en4.bsa", "Skyrim - Voices_en0.bsa",
}

type line struct {
	archive *bsa.Archive
	path    string
	size    int
}

func main() {
	seconds := flag.Float64("seconds", 60, "seconds of reference audio")
	minimum := flag.Float64("min", 3, "shortest line to keep")
	maximum := flag.Float64("max", 9, "longest line to keep")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: voice-ref [--seconds N] [--min N] [--max N] VOICE OUT")
		os.Exit(2)
	}
	if err := build(flag.Arg(0), flag.Arg(1), *seconds, *minimum, *maximum); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(voice, out string, seconds, minimum, maximum float64) error {
	var lines []line
	for _, name := range archives {
		path := filepath.Join(gameData, name)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		archive, err := bsa.Open(path)
		if err != nil {
			return err
		}
		var hits []string
		for _, path := range archive.Order {
			if strings.Contains(strings.ToLower(path), strings.ToLower(voice)) {
				hits = append(hits, path)
			}
		}
		if len(hits) == 0 {
			continue
		}
		fmt.Printf("%s: %d lines\n", name, len(hits))
		for _, path := range hits {
			data, err := archive.Read(path)
			if err != nil {
				return err
			}
			lines = append(lines, line{archive: archive, path: path, size: len(data)})
		}
		break
	}
	if len(lines) == 0 {
		return fmt.Errorf("no lines for voice type %q in %s", voice, gameData)
	}
	slices.SortStableFunc(lines, func(a, b line) int { return b.size - a.size })
	tmp, err := os.MkdirTemp("", "soulgem-voice-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	var picked []string
	total := 0.0
	for i, current := range lines {
		if total >= seconds {
			break
		}
		wav := filepath.Join(tmp, fmt.Sprintf("%04d.wav", i))
		fuz, err := current.archive.Read(current.path)
		if err != nil {
			return err
		}
		duration, err := decode(fuz, wav)
		if err != nil {
			return err
		}
		if duration < minimum || duration > maximum {
			continue
		}
		picked = append(picked, wav)
		total += duration
		fmt.Printf("  + %4.1fs  %s\n", duration, filepath.Base(current.path))
	}
	listing := filepath.Join(tmp, "list.txt")
	file, err := os.Create(listing)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	for _, wav := range picked {
		fmt.Fprintf(writer, "file '%s'\n", wav)
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "concat", "-safe", "0",
		"-i", listing, "-t", strconv.FormatFloat(seconds, 'g', -1, 64),
		"-ar", "24000", "-ac", "1", "-c:a", "pcm_s16le", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg concat: %w: %s", err, output)
	}
	fmt.Printf("\nwrote %s: %vs from %d lines\n", out, seconds, len(picked))
	return nil
}

func decode(fuz []byte, out string) (float64, error) {
	if len(fuz) < 12 {
		return 0, fmt.Errorf("truncated FUZ")
	}
	lip := int(binary.LittleEndian.Uint32(fuz[8:12]))
	if len(fuz) < 12+lip {
		return 0, fmt.Errorf("truncated FUZ lip data")
	}
	xwm := out + ".xwm"
	if err := os.WriteFile(xwm, fuz[12+lip:], 0o644); err != nil {
		return 0, err
	}
	defer os.Remove(xwm)
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-i", xwm,
		"-ar", "24000", "-ac", "1", "-c:a", "pcm_s16le", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("ffmpeg decode: %w: %s", err, output)
	}
	cmd = exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", out)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
}
