package gltf

import (
	"encoding/binary"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"soulgem/internal/mathutil"
)

// TestMarshalKeyOrderAndFloatSpelling is the golden test for the compatibility
// the GLB hashes depend on: keys in declaration order, floats that always carry
// a decimal point, and integers that never grow one.
func TestMarshalKeyOrderAndFloatSpelling(t *testing.T) {
	cutoff := Float(0.5019607843137255)
	document := Document{
		Asset:  Asset{Version: "2.0", Generator: "lydia nif2gltf"},
		Scene:  0,
		Scenes: []Scene{{Nodes: []int{2, 0, 1}}},
		Skins:  []Skin{{Joints: []int{0}, InverseBindMatrices: 3, Skeleton: 0}},
		Nodes: []Node{
			{
				Name:        "NPC Root [Root]",
				Translation: Floats(0, 0, 0),
				Rotation:    Floats(0, 0, 0, 1),
				Scale:       Floats(1, 1, 1),
				Children:    []int{1},
			},
			{Name: "Body", Mesh: pointer(0), Skin: pointer(0)},
			{Name: "Lydia", Children: []int{0}, Matrix: []Number{
				Decimal(-0.01428), Int(0), Int(0), Int(0),
				Int(0), Int(0), Decimal(0.01428), Int(0),
				Int(0), Decimal(0.01428), Int(0), Int(0),
				Int(0), Int(0), Int(0), Int(1),
			}},
		},
		Meshes: []Mesh{{
			Name: "Body",
			Primitives: []Primitive{{
				Attributes: Attributes{Position: 0, Normal: 1, TexCoord0: pointer(2)},
				Indices:    3,
				Material:   0,
				Targets:    []MorphTarget{{Position: 4}},
			}},
			Weights: Floats(0),
			Extras:  &Extras{TargetNames: []string{"Aah"}},
		}},
		Materials: []Material{{
			Name: "Body",
			PBR: PBR{
				MetallicFactor: 0, RoughnessFactor: 0.55,
				BaseColorFactor:  Floats(0.23, 0.12, 0.1, 1),
				BaseColorTexture: &TextureRef{Index: 0},
			},
			DoubleSided: true,
			AlphaMode:   "MASK",
			AlphaCutoff: &cutoff,
		}},
		Accessors: []Accessor{
			{BufferView: 0, ComponentType: 5126, Count: 1, Type: "VEC3",
				Min: Floats(0, 1, 2), Max: Floats(0, 1, 2)},
			{BufferView: 1, ComponentType: 5126, Count: 1, Type: "VEC3"},
		},
		BufferViews: []BufferView{
			{Buffer: 0, ByteOffset: 0, ByteLength: 12, Target: 34962},
			{Buffer: 0, ByteOffset: 12, ByteLength: 8},
		},
		Samplers: []Sampler{DefaultSampler()},
		Images:   []Image{{BufferView: 2, MimeType: "image/png", Name: "skin.png"}},
		Textures: []Texture{{Sampler: 0, Source: 0}},
		Buffers:  []Buffer{{ByteLength: 20}},
	}
	got, err := Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"asset":{"version":"2.0","generator":"lydia nif2gltf"},"scene":0,` +
		`"scenes":[{"nodes":[2,0,1]}],` +
		`"skins":[{"joints":[0],"inverseBindMatrices":3,"skeleton":0}],` +
		`"nodes":[` +
		`{"name":"NPC Root [Root]","translation":[0.0,0.0,0.0],"rotation":[0.0,0.0,0.0,1.0],"scale":[1.0,1.0,1.0],"children":[1]},` +
		`{"name":"Body","mesh":0,"skin":0},` +
		`{"name":"Lydia","children":[0],"matrix":[-0.01428,0,0,0,0,0,0.01428,0,0,0.01428,0,0,0,0,0,1]}],` +
		`"meshes":[{"name":"Body","primitives":[{"attributes":{"POSITION":0,"NORMAL":1,"TEXCOORD_0":2},` +
		`"indices":3,"material":0,"targets":[{"POSITION":4}]}],"weights":[0.0],"extras":{"targetNames":["Aah"]}}],` +
		`"materials":[{"name":"Body","pbrMetallicRoughness":{"metallicFactor":0.0,"roughnessFactor":0.55,` +
		`"baseColorFactor":[0.23,0.12,0.1,1.0],"baseColorTexture":{"index":0}},"doubleSided":true,` +
		`"alphaMode":"MASK","alphaCutoff":0.5019607843137255}],` +
		`"accessors":[{"bufferView":0,"componentType":5126,"count":1,"type":"VEC3","min":[0.0,1.0,2.0],"max":[0.0,1.0,2.0]},` +
		`{"bufferView":1,"componentType":5126,"count":1,"type":"VEC3"}],` +
		`"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":12,"target":34962},` +
		`{"buffer":0,"byteOffset":12,"byteLength":8}],` +
		`"samplers":[{"magFilter":9729,"minFilter":9987,"wrapS":10497,"wrapT":10497}],` +
		`"images":[{"bufferView":2,"mimeType":"image/png","name":"skin.png"}],` +
		`"textures":[{"sampler":0,"source":0}],"buffers":[{"byteLength":20}]}`
	if string(got) != want {
		t.Errorf("Marshal()\n got %s\nwant %s", got, want)
	}
	// Whatever the spelling, it has to stay valid JSON.
	if !json.Valid(got) {
		t.Error("output is not valid JSON")
	}
}

// TestByteOffsetZeroIsEmitted guards a mistake that would be invisible in the
// first buffer view and silently corrupt every later one: omitting a zero
// offset because it looks empty.
func TestByteOffsetZeroIsEmitted(t *testing.T) {
	got, err := Marshal(BufferView{Buffer: 0, ByteOffset: 0, ByteLength: 4})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"buffer":0,"byteOffset":0,"byteLength":4}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestViewsAreFourByteAligned(t *testing.T) {
	w := NewWriter("test")
	if got := w.AddView([]byte{1, 2, 3}, 0); got != 0 {
		t.Fatalf("first view index = %d", got)
	}
	w.AddView([]byte{4, 5}, 34962)
	views := w.Document.BufferViews
	if views[0].ByteOffset != 0 || views[0].ByteLength != 3 {
		t.Errorf("view 0 = %+v", views[0])
	}
	// The second view starts at 4, not 3: glTF requires four-byte alignment.
	if views[1].ByteOffset != 4 || views[1].ByteLength != 2 {
		t.Errorf("view 1 = %+v", views[1])
	}
	if views[1].Target != 34962 {
		t.Errorf("target = %d", views[1].Target)
	}
}

func TestPositionAccessorCarriesBounds(t *testing.T) {
	w := NewWriter("test")
	index := w.AddPositions([]mathutil.Vec3{{1, -2, 3}, {-4, 5, -6}})
	accessor := w.Document.Accessors[index]
	if accessor.Count != 2 || accessor.Type != "VEC3" || accessor.ComponentType != 5126 {
		t.Errorf("accessor = %+v", accessor)
	}
	if want := Floats(-4, -2, -6); !slices.Equal(accessor.Min, want) {
		t.Errorf("Min = %v, want %v", accessor.Min, want)
	}
	if want := Floats(1, 5, 3); !slices.Equal(accessor.Max, want) {
		t.Errorf("Max = %v, want %v", accessor.Max, want)
	}
}

func TestImagesAreDeduplicated(t *testing.T) {
	w := NewWriter("test")
	first := w.AddImage("skin.png", []byte("png-a"), "/cache/skin.png")
	again := w.AddImage("skin.png", []byte("png-a"), "/cache/skin.png")
	other := w.AddImage("hair.png", []byte("png-b"), "/cache/hair.png")
	if first != again {
		t.Errorf("the same image produced textures %d and %d", first, again)
	}
	if other == first {
		t.Error("different images should get different textures")
	}
	if len(w.Document.Images) != 2 || len(w.Document.BufferViews) != 2 {
		t.Errorf("got %d images across %d views", len(w.Document.Images), len(w.Document.BufferViews))
	}
}

func TestGLBContainer(t *testing.T) {
	w := NewWriter("soulgem")
	w.AddView([]byte{1, 2, 3, 4, 5}, 0) // deliberately not a multiple of four
	data, err := w.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data[0:4]) != "glTF" {
		t.Fatalf("magic = %q", data[0:4])
	}
	if got := binary.LittleEndian.Uint32(data[4:]); got != 2 {
		t.Errorf("version = %d", got)
	}
	if got := int(binary.LittleEndian.Uint32(data[8:])); got != len(data) {
		t.Errorf("declared length %d, actual %d", got, len(data))
	}
	jsonLength := int(binary.LittleEndian.Uint32(data[12:]))
	if string(data[16:20]) != "JSON" {
		t.Fatalf("first chunk = %q", data[16:20])
	}
	if jsonLength%4 != 0 {
		t.Errorf("JSON chunk length %d is not four-byte aligned", jsonLength)
	}
	jsonChunk := data[20 : 20+jsonLength]
	if !json.Valid([]byte(strings.TrimRight(string(jsonChunk), " "))) {
		t.Error("JSON chunk does not parse")
	}
	rest := data[20+jsonLength:]
	binLength := int(binary.LittleEndian.Uint32(rest))
	if string(rest[4:8]) != "BIN\x00" {
		t.Fatalf("second chunk = %q", rest[4:8])
	}
	if binLength != 8 {
		t.Errorf("binary chunk length = %d, want 5 bytes padded to 8", binLength)
	}
	if len(rest[8:]) != binLength {
		t.Errorf("binary chunk holds %d bytes, header says %d", len(rest[8:]), binLength)
	}
	// The declared buffer length is the unpadded size the views describe.
	if got := w.Document.Buffers[0].ByteLength; got != 5 {
		t.Errorf("buffer byteLength = %d, want 5", got)
	}
}

func TestMarshalRejectsUnsupportedTypes(t *testing.T) {
	if _, err := Marshal(struct {
		Bad chan int `json:"bad"`
	}{}); err == nil {
		t.Fatal("expected an error for an unencodable field")
	}
}

func pointer[T any](value T) *T { return &value }
