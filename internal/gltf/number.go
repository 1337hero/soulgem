package gltf

import (
	"bytes"
	"encoding/json"
	"strconv"

	"soulgem/internal/pyjson"
)

// Float is a float64 that serialises the way Python's json.dumps wrote it:
// always with a decimal point, so 0 stays "0.0". The GLB files are checked by
// hash, so the spelling matters.
type Float float64

// Floats converts plain float64s at the boundary into the document's spelling.
func Floats(values ...float64) []Float {
	out := make([]Float, len(values))
	for i, value := range values {
		out[i] = Float(value)
	}
	return out
}

func (f Float) MarshalJSON() ([]byte, error) { return pyjson.AppendFloat(nil, float64(f)), nil }

// Number is a JSON number that keeps its spelling. The root node's matrix mixes
// integer and float literals because Python built it from both.
type Number struct {
	text string
}

func Int(value int) Number         { return Number{strconv.Itoa(value)} }
func Decimal(value float64) Number { return Number{string(pyjson.AppendFloat(nil, value))} }

func (n Number) MarshalJSON() ([]byte, error) { return []byte(n.text), nil }

// Marshal encodes a document as compact JSON. Struct declaration order is the
// key order, which is why the types in gltf.go are declared in the order glTF
// files are written.
func Marshal(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	// Python did not escape <, > or &, and Encoder appends a newline.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}
