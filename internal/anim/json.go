package anim

import (
	"strconv"

	"soulgem/internal/pyjson"
)

// MarshalPythonJSON writes the clip the way the Python exporter did: no spaces,
// Python's repr-style float formatting, and bones in track order. The viewer
// reads these files directly, and the format is frozen by the committed clips
// under anims/.
func (c *Clip) MarshalPythonJSON() []byte {
	out := []byte(`{"duration":`)
	out = pyjson.AppendFloat(out, c.Duration)
	out = append(out, `,"fps":`...)
	out = pyjson.AppendFloat(out, c.FPS)
	out = append(out, `,"frames":`...)
	out = strconv.AppendInt(out, int64(c.Frames), 10)
	out = append(out, `,"bones":{`...)
	for i, name := range c.Order {
		if i != 0 {
			out = append(out, ',')
		}
		bone := c.Bones[name]
		out = pyjson.AppendString(out, name)
		out = append(out, `:{"pos":`...)
		out = appendVec3s(out, bone.Pos)
		out = append(out, `,"rot":`...)
		out = appendVec4s(out, bone.Rot)
		if bone.Scale != nil {
			out = append(out, `,"scale":`...)
			out = appendVec3s(out, bone.Scale)
		}
		out = append(out, '}')
	}
	return append(out, "}}"...)
}

func appendVec3s(out []byte, values [][3]float64) []byte {
	return appendTuples(out, len(values), func(i int) []float64 { return values[i][:] })
}

func appendVec4s(out []byte, values [][4]float64) []byte {
	return appendTuples(out, len(values), func(i int) []float64 { return values[i][:] })
}

// appendTuples writes a list of float tuples as nested JSON arrays.
func appendTuples(out []byte, count int, at func(int) []float64) []byte {
	out = append(out, '[')
	for i := range count {
		if i != 0 {
			out = append(out, ',')
		}
		out = append(out, '[')
		for j, component := range at(i) {
			if j != 0 {
				out = append(out, ',')
			}
			out = pyjson.AppendFloat(out, component)
		}
		out = append(out, ']')
	}
	return append(out, ']')
}
