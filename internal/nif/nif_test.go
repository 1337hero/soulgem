package nif

import (
	"strings"
	"testing"

	"soulgem/internal/mathutil"
)

// skinnedFixture is one skinned BSTriShape weighted to two bones, with a
// shader, a texture set and alpha. Vertex 0 is weighted entirely to the second
// bone in the skin's bone list while the partition's bone palette lists the
// bones in the opposite order — that is the case the SSE global-index rule has
// to get right.
func skinnedFixture() ([]byte, *builder) {
	b := &builder{}
	shape := 0 // filled in below; blocks are referenced by index
	_ = shape

	textures := b.textureSet(`textures\actors\skin.dds`, `textures\actors\skin_n.dds`)
	shader := b.shader("Skin", 0, textures, 42, [3]float64{0.25, 0.5, 0.75})
	alphaProp := b.alpha(0x200, 128)

	// Bones must be nodes so the parser can resolve their names and globals.
	boneA := b.node("Bone A")
	boneB := b.node("Bone B")

	skinData := b.skinData(2, []int{0, 0})
	partition := b.skinPartition([]partitionSpec{{
		numVerts:    3,
		bonePalette: []uint16{1, 0}, // deliberately not the skin's own order
		triangles:   [][3]uint16{{0, 1, 2}},
	}})
	skin := b.skinInstance("BSDismemberSkinInstance", skinData, partition, []int{boneA, boneB})

	b.triShape(shapeSpec{
		name: "Body",
		vertices: []vertex{
			{position: [3]float64{1, 2, 3}, uv: [2]float64{0, 0.5},
				normal: [3]uint8{255, 128, 0}, weights: [4]float64{1, 0, 0, 0}, bones: [4]uint8{1, 0, 0, 0}},
			{position: [3]float64{4, 5, 6}, uv: [2]float64{0.5, 1},
				normal: [3]uint8{128, 255, 0}, weights: [4]float64{0.5, 0.5, 0, 0}, bones: [4]uint8{0, 1, 0, 0}},
			{position: [3]float64{7, 8, 9}, uv: [2]float64{1, 0},
				normal: [3]uint8{0, 128, 255}, weights: [4]float64{1, 0, 0, 0}, bones: [4]uint8{0, 0, 0, 0}},
		},
		triangles: [][3]uint16{{0, 1, 2}},
		skinRef:   skin, shaderRef: shader, alphaRef: alphaProp,
	})
	b.node("Scene Root", boneA, boneB)
	return b.bytes(), b
}

func TestParseSkinnedShape(t *testing.T) {
	data, _ := skinnedFixture()
	file, err := Parse(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if file.Stream != 100 {
		t.Errorf("Stream = %d", file.Stream)
	}
	if len(file.Shapes) != 1 {
		t.Fatalf("got %d shapes", len(file.Shapes))
	}
	shape := file.Shapes[0]
	if shape.Name != "Body" {
		t.Errorf("Name = %q", shape.Name)
	}
	if !shape.Skinned {
		t.Error("shape should be skinned")
	}
	if len(shape.Positions) != 3 {
		t.Fatalf("got %d positions", len(shape.Positions))
	}
	// Identity bind transforms mean the baked positions are the authored ones.
	if shape.Positions[0] != (mathutil.Vec3{1, 2, 3}) || shape.Positions[2] != (mathutil.Vec3{7, 8, 9}) {
		t.Errorf("Positions = %v", shape.Positions)
	}
	if len(shape.UVs) != 3 || shape.UVs[1] != [2]float64{0.5, 1} {
		t.Errorf("UVs = %v", shape.UVs)
	}
	if len(shape.Normals) != 3 {
		t.Errorf("got %d normals", len(shape.Normals))
	}
	// Triangles come from the shape's index buffer and again from the
	// partition, which is what the game ships.
	if len(shape.Triangles) != 2 {
		t.Errorf("Triangles = %v", shape.Triangles)
	}
	checkSkinnedMaterial(t, shape)
	// Skinning replaces the shape transform with baked bind-pose geometry.
	if shape.Transform != nil {
		t.Error("a skinned shape should have no residual transform")
	}
}

func checkSkinnedMaterial(t *testing.T, shape *Shape) {
	t.Helper()
	if len(shape.Textures) != 2 || shape.Textures[0] != `textures\actors\skin.dds` {
		t.Errorf("Textures = %v", shape.Textures)
	}
	if shape.Glossiness != 42 || shape.Specular != (mathutil.Vec3{0.25, 0.5, 0.75}) {
		t.Errorf("Glossiness = %v, Specular = %v", shape.Glossiness, shape.Specular)
	}
	if shape.AlphaFlags == nil || *shape.AlphaFlags != 0x200 || shape.AlphaThreshold != 128 {
		t.Errorf("AlphaFlags = %v, threshold = %d", shape.AlphaFlags, shape.AlphaThreshold)
	}
}

// TestSSEBoneIndicesAreGlobal pins the rule the exporter depends on: a vertex's
// bone byte indexes the skin instance's bone list, not the partition's palette.
// Reading it as palette-relative silently attaches vertices to the wrong bone,
// which shows up as a limb that follows the wrong joint.
func TestSSEBoneIndicesAreGlobal(t *testing.T) {
	data, _ := skinnedFixture()
	file, err := Parse(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	weights := file.Shapes[0].SkinWeights
	if len(weights) != 3 {
		t.Fatalf("got %d weighted vertices", len(weights))
	}
	if len(weights[0]) != 1 || weights[0][0].Bone != "Bone B" || weights[0][0].Weight != 1 {
		t.Fatalf("vertex 0 = %v; bone byte 1 must resolve through the skin's bone "+
			"list to Bone B, not through the partition palette to Bone A", weights[0])
	}
	if len(weights[1]) != 2 || weights[1][0].Bone != "Bone A" || weights[1][1].Bone != "Bone B" {
		t.Errorf("vertex 1 = %v", weights[1])
	}
	if weights[1][0].Weight != 0.5 || weights[1][1].Weight != 0.5 {
		t.Errorf("vertex 1 weights = %v", weights[1])
	}
	if _, ok := file.Shapes[0].BoneMatrices["Bone A"]; !ok {
		t.Error("BoneMatrices should name every bone in the skin")
	}
}

func TestNodeTreeAndGlobals(t *testing.T) {
	b := &builder{}
	child := b.node("Child")
	b.node("Root", child)
	file, err := Parse(b.bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := file.NodeTree["Child"].Parent; got != "Root" {
		t.Errorf("Child's parent = %q", got)
	}
	if got := file.NodeTree["Root"].Parent; got != "" {
		t.Errorf("Root's parent = %q, want none", got)
	}
	if len(file.NodeOrder) != 2 {
		t.Errorf("NodeOrder = %v", file.NodeOrder)
	}
	if _, ok := file.NodeGlobals["Child"]; !ok {
		t.Error("every node needs a global transform")
	}
}

// TestSkeletonOverridesBoneGlobals covers the reason meshes are parsed with the
// shared skeleton: a mesh carries its own copy of the bone poses, and the
// skeleton file's copy is the one every mesh must agree on.
func TestSkeletonOverridesBoneGlobals(t *testing.T) {
	data, _ := skinnedFixture()
	shifted := mathutil.Mat4{{1, 0, 0, 10}, {0, 1, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}}
	file, err := Parse(data, map[string]mathutil.Mat4{"Bone B": shifted})
	if err != nil {
		t.Fatal(err)
	}
	// Vertex 0 is weighted entirely to Bone B, so the override moves it.
	if got := file.Shapes[0].Positions[0]; got != (mathutil.Vec3{11, 2, 3}) {
		t.Errorf("Positions[0] = %v, want the skeleton's Bone B pose applied", got)
	}
}

func TestParseRejectsBadHeaders(t *testing.T) {
	data, _ := skinnedFixture()
	cases := []struct {
		name  string
		data  []byte
		match string
	}{
		{"no newline", []byte("Gamebryo"), "unsupported NIF header"},
		{"wrong version string", []byte("Gamebryo File Format, Version 20.0.0.5\n"), "unsupported NIF header"},
		{"truncated after the header line", []byte("Gamebryo File Format, Version 20.2.0.7\n"), "offset"},
		{"empty", nil, "unsupported NIF header"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.data, nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.match) {
				t.Fatalf("err = %q, want it to mention %q", err, test.match)
			}
		})
	}
	// Every truncation of a real file must produce an error, not a panic.
	for cut := 1; cut < len(data); cut += 13 {
		if _, err := Parse(data[:cut], nil); err == nil {
			t.Fatalf("truncating to %d of %d bytes parsed successfully", cut, len(data))
		}
	}
}

// TestParseRejectsCorruptCounts flips bytes throughout the file and requires
// that the parser either succeeds or returns an error. A panic here would be a
// crash on a malformed asset.
func TestParseRejectsCorruptCounts(t *testing.T) {
	original, _ := skinnedFixture()
	for i := range original {
		data := append([]byte(nil), original...)
		data[i] ^= 0xff
		func() {
			defer func() {
				if problem := recover(); problem != nil {
					t.Fatalf("byte %d flipped: panic %v", i, problem)
				}
			}()
			_, _ = Parse(data, nil)
		}()
	}
}

func TestUnknownShaderIsRecordedByName(t *testing.T) {
	b := &builder{}
	effect := b.add("BSEffectShaderProperty", make([]byte, 64))
	b.triShape(shapeSpec{
		name:      "Glow",
		vertices:  []vertex{{position: [3]float64{0, 0, 0}}},
		triangles: [][3]uint16{{0, 0, 0}},
		skinRef:   -1, shaderRef: effect, alphaRef: -1,
	})
	file, err := Parse(b.bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Shapes[0].ShaderType; got != "BSEffectShaderProperty" {
		t.Errorf("ShaderType = %v", got)
	}
}
