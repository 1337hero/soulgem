package anim

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// node is a generic XML element. hkxc's output is a deep tree of <hkobject>
// wrappers whose shape depends on the source file, so it is walked generically
// rather than mapped to structs.
type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Content  string     `xml:",chardata"`
	Children []node     `xml:",any"`
}

func (n *node) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (n *node) find(match func(*node) bool) *node {
	if match(n) {
		return n
	}
	for i := range n.Children {
		if hit := n.Children[i].find(match); hit != nil {
			return hit
		}
	}
	return nil
}

func (n *node) walk(visit func(*node)) {
	visit(n)
	for i := range n.Children {
		n.Children[i].walk(visit)
	}
}

// params flattens an hkobject's direct <hkparam> children into a lookup, and
// reads them with errors that name the missing or malformed field.
type params struct {
	values map[string]string
	err    error
}

func (p *params) integer(name string) int {
	value, err := strconv.Atoi(p.text(name))
	if err != nil && p.err == nil {
		p.err = fmt.Errorf("%s: %w", name, err)
	}
	return value
}

func (p *params) float(name string) float64 {
	value, err := strconv.ParseFloat(p.text(name), 64)
	if err != nil && p.err == nil {
		p.err = fmt.Errorf("%s: %w", name, err)
	}
	return value
}

func (p *params) text(name string) string {
	value, ok := p.values[name]
	if !ok && p.err == nil {
		p.err = fmt.Errorf("%s is missing", name)
	}
	return value
}

// ints parses a whitespace-separated integer list, which is how hkxc writes
// both blockOffsets and the spline data itself.
func (p *params) ints(name string) []int {
	fields := strings.Fields(p.text(name))
	out := make([]int, len(fields))
	for i, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			if p.err == nil {
				p.err = fmt.Errorf("%s[%d]: %w", name, i, err)
			}
			return nil
		}
		out[i] = value
	}
	return out
}

func parseXML(data []byte) (*Info, error) {
	var root node
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// hkxc emits an encoding declaration Go's decoder does not know; the bytes
	// are ASCII regardless.
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	animation := root.find(func(n *node) bool {
		return n.XMLName.Local == "hkobject" && n.attr("class") == "hkaSplineCompressedAnimation"
	})
	if animation == nil {
		return nil, fmt.Errorf("no spline-compressed animation found")
	}

	p := &params{values: make(map[string]string)}
	for _, child := range animation.Children {
		if child.XMLName.Local == "hkparam" {
			p.values[child.attr("name")] = strings.TrimSpace(child.Content)
		}
	}
	info := &Info{
		Duration:          p.float("duration"),
		FrameDuration:     p.float("frameDuration"),
		NumFrames:         p.integer("numFrames"),
		NumTracks:         p.integer("numberOfTransformTracks"),
		NumFloatTracks:    p.integer("numberOfFloatTracks"),
		NumBlocks:         p.integer("numBlocks"),
		MaxFramesPerBlock: p.integer("maxFramesPerBlock"),
		BlockOffsets:      p.ints("blockOffsets"),
	}
	raw := p.ints("data")
	if p.err != nil {
		return nil, p.err
	}
	info.Data = make([]byte, len(raw))
	for i, value := range raw {
		info.Data[i] = byte(value)
	}
	animation.walk(func(n *node) {
		if n.attr("name") == "trackName" {
			// hkxc renders a blank (all-NUL) track name as U+2400 (␀) rather
			// than an empty string; strip it so these clips take the same
			// positional skeleton_track_order fallback as vanilla ones. Left
			// as-is, 97 identical "␀" names collapse to a single bone.
			name := strings.TrimSpace(strings.ReplaceAll(n.Content, "␀", ""))
			info.TrackNames = append(info.TrackNames, name)
		}
	})
	if err := info.validate(); err != nil {
		return nil, err
	}
	return info, nil
}

// validate rejects headers whose counts cannot describe a real clip, so the
// spline decoder can assume its loop bounds are sane.
func (info *Info) validate() error {
	switch {
	case info.NumFrames <= 0:
		return fmt.Errorf("numFrames is %d", info.NumFrames)
	case info.NumTracks < 0:
		return fmt.Errorf("numberOfTransformTracks is %d", info.NumTracks)
	case info.NumFloatTracks < 0:
		return fmt.Errorf("numberOfFloatTracks is %d", info.NumFloatTracks)
	case info.MaxFramesPerBlock <= 1:
		return fmt.Errorf("maxFramesPerBlock is %d, need at least 2", info.MaxFramesPerBlock)
	case info.FrameDuration <= 0:
		return fmt.Errorf("frameDuration is %v", info.FrameDuration)
	case len(info.BlockOffsets) == 0:
		return fmt.Errorf("animation has no blocks")
	}
	return nil
}
