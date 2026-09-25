package main

import (
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
	"soulgem/internal/character"
)

var archives = []string{
	"UHDAP - en0.bsa", "UHDAP - en1.bsa", "UHDAP - en2.bsa", "UHDAP - en3.bsa",
	"UHDAP - en4.bsa", "SkyrimSE - Voices_en0.bsa",
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

func voiceLines(voice string) ([]line, error) {
	var lines []line
	for _, name := range archives {
		path := filepath.Join(character.Data, name)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		archive, err := bsa.Open(path)
		if err != nil {
			return nil, err
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
				return nil, err
			}
			lines = append(lines, line{archive: archive, path: path, size: len(data)})
		}
		break
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no lines for voice type %q in %s", voice, character.Data)
	}
	return lines, nil
}

func build(voice, out string, seconds, minimum, maximum float64) error {
	lines, err := voiceLines(voice)
	if err != nil {
		return err
	}
	slices.SortStableFunc(lines, func(a, b line) int { return b.size - a.size })
	tmp, err := os.MkdirTemp("", "soulgem-voice-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	picked, err := pickLines(lines, tmp, seconds, minimum, maximum)
	if err != nil {
		return err
	}
	if err := concatenate(picked, tmp, out, seconds); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s: %vs from %d lines\n", out, seconds, len(picked))
	return nil
}

func pickLines(lines []line, tmp string, seconds, minimum, maximum float64) ([]string, error) {
	var picked []string
	total := 0.0
	for i, current := range lines {
		if total >= seconds {
			break
		}
		wav := filepath.Join(tmp, fmt.Sprintf("%04d.wav", i))
		fuz, err := current.archive.Read(current.path)
		if err != nil {
			return nil, err
		}
		duration, err := decode(fuz, wav)
		if err != nil {
			return nil, err
		}
		if duration < minimum || duration > maximum {
			continue
		}
		picked = append(picked, wav)
		total += duration
		fmt.Printf("  + %4.1fs  %s\n", duration, filepath.Base(current.path))
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("no voice lines within the requested duration range")
	}
	return picked, nil
}

func concatenate(picked []string, tmp, out string, seconds float64) error {
	listing := filepath.Join(tmp, "list.txt")
	var listingText strings.Builder
	for _, wav := range picked {
		fmt.Fprintf(&listingText, "file '%s'\n", wav)
	}
	if err := os.WriteFile(listing, []byte(listingText.String()), 0o600); err != nil {
		return err
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "concat", "-safe", "0",
		"-i", listing, "-t", strconv.FormatFloat(seconds, 'g', -1, 64),
		"-ar", "24000", "-ac", "1", "-c:a", "pcm_s16le", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg concat: %w: %s", err, output)
	}
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
