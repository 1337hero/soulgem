package glb

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soulgem/internal/mathutil"
)

// scene builds a two-joint, two-part character: one textured skinned part with
// morph targets and one untextured tinted part. It exercises every branch the
// writer has without needing the game installed.
func scene(t *testing.T, pngPath string) *Scene {
	t.Helper()
	return &Scene{
		Name:  "Test",
		Roots: []int{0},
		Joints: []Joint{
			{
				Name: "NPC Root [Root]", Children: []int{1},
				Translation: mathutil.Vec3{0, 0, 0}, Rotation: [4]float64{0, 0, 0, 1}, Scale: 1,
				InverseBind: [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
			},
			{
				Name:        "NPC Head [Head]",
				Translation: mathutil.Vec3{0, 0, 10}, Rotation: [4]float64{0, 0, 0, 1}, Scale: 1,
				InverseBind: [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, -10, 1},
			},
		},
		Parts: []Part{
			{
				Source:    "meshes/head.nif",
				Name:      "Head",
				Positions: []mathutil.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
				Normals:   []mathutil.Vec3{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
				UVs:       [][2]float64{{0, 0}, {1, 0}, {0, 1}},
				Triangles: [][3]uint16{{0, 1, 2}},
				Joints:    [][4]uint16{{1, 0, 0, 0}, {1, 0, 0, 0}, {1, 0, 0, 0}},
				Weights:   [][4]float64{{1, 0, 0, 0}, {1, 0, 0, 0}, {1, 0, 0, 0}},
				Material: Material{
					Name: "Head", PNG: pngPath, Texture: "/game/textures/femalehead.dds",
					AlphaMode: "MASK", AlphaCutoff: 0.5,
				},
				Morphs: []Morph{
					{Name: "Aah", Deltas: []mathutil.Vec3{{0, 0, 1}, {0, 0, 0}, {0, 0, 0}}},
					{Name: "Eee", Deltas: []mathutil.Vec3{{0, 1, 0}, {0, 0, 0}, {0, 0, 0}}},
				},
			},
			{
				Source:    "meshes/hair.nif",
				Name:      "Hair",
				Positions: []mathutil.Vec3{{0, 0, 0}, {1, 1, 1}, {2, 2, 2}},
				Normals:   []mathutil.Vec3{{0, 1, 0}, {0, 1, 0}, {0, 1, 0}},
				Triangles: [][3]uint16{{0, 1, 2}},
				Material: Material{
					Name:            "Hair",
					BaseColorFactor: []float64{0.035, 0.025, 0.018, 1},
				},
			},
		},
	}
}

func writePNG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "femalehead.a1b2c3d4.png")
	// Only the bytes matter here; nothing in the pipeline decodes the image.
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nnot-a-real-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildScene(t *testing.T) ([]byte, Report, map[string]any) {
	t.Helper()
	data, report, err := write(scene(t, writePNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	length := int(binary.LittleEndian.Uint32(data[12:]))
	var document map[string]any
	if err := json.Unmarshal([]byte(strings.TrimRight(string(data[20:20+length]), " ")), &document); err != nil {
		t.Fatal(err)
	}
	return data, report, document
}

func TestWriteSceneStructure(t *testing.T) {
	_, report, document := buildScene(t)

	nodes := document["nodes"].([]any)
	if len(nodes) != 5 { // two joints, two mesh nodes, one root
		t.Fatalf("got %d nodes", len(nodes))
	}
	root := nodes[4].(map[string]any)
	if root["name"] != "Test" {
		t.Errorf("last node = %v, want the scene root", root["name"])
	}
	if _, ok := root["matrix"]; !ok {
		t.Error("the scene root carries the axis-conversion matrix")
	}
	// The scene lists the root first, then every mesh node.
	sceneNodes := document["scenes"].([]any)[0].(map[string]any)["nodes"].([]any)
	if len(sceneNodes) != 3 || sceneNodes[0].(float64) != 4 {
		t.Errorf("scene nodes = %v", sceneNodes)
	}

	// Only the skinned part gets a skin reference.
	head := nodes[2].(map[string]any)
	if head["name"] != "Head" || head["skin"] != float64(0) {
		t.Errorf("head node = %v", head)
	}
	hair := nodes[3].(map[string]any)
	if _, ok := hair["skin"]; ok {
		t.Error("an unskinned part should not reference the skin")
	}

	skin := document["skins"].([]any)[0].(map[string]any)
	if joints := skin["joints"].([]any); len(joints) != 2 {
		t.Errorf("joints = %v", joints)
	}
	if skin["skeleton"] != float64(0) {
		t.Errorf("skeleton = %v", skin["skeleton"])
	}

	if report.Meshes != 2 || report.Textures != 1 {
		t.Errorf("report = %+v", report)
	}
	if len(report.Parts) != 2 {
		t.Fatalf("report parts = %v", report.Parts)
	}
	if report.Parts[0] != (PartReport{
		Source: "head.nif", Name: "Head", Vertices: 3,
		Texture: "femalehead.dds", MorphTargets: 2,
	}) {
		t.Errorf("head report = %+v", report.Parts[0])
	}
	if report.Parts[1].Texture != "None" || report.Parts[1].MorphTargets != 0 {
		t.Errorf("hair report = %+v", report.Parts[1])
	}
}

func TestWriteSceneAccessorOrder(t *testing.T) {
	_, _, document := buildScene(t)
	// The order accessors are added in decides the binary layout, so it is
	// pinned: position, normal, UV, joints, weights, indices, then morphs.
	accessors := document["accessors"].([]any)
	types := make([]string, len(accessors))
	for i, accessor := range accessors {
		types[i] = accessor.(map[string]any)["type"].(string)
	}
	want := []string{
		"VEC3", "VEC3", "VEC2", "VEC4", "VEC4", "SCALAR", // head
		"VEC3", "VEC3", // head morph targets
		"VEC3", "VEC3", "SCALAR", // hair: no UVs, no skin
		"MAT4", // inverse bind matrices, added last
	}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Errorf("accessor types =\n %v\nwant %v", types, want)
	}

	head := document["meshes"].([]any)[0].(map[string]any)
	attributes := head["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	for _, name := range []string{"POSITION", "NORMAL", "TEXCOORD_0", "JOINTS_0", "WEIGHTS_0"} {
		if _, ok := attributes[name]; !ok {
			t.Errorf("head is missing %s", name)
		}
	}
	if names := head["extras"].(map[string]any)["targetNames"].([]any); len(names) != 2 || names[0] != "Aah" {
		t.Errorf("targetNames = %v", names)
	}
	if weights := head["weights"].([]any); len(weights) != 2 || weights[0] != float64(0) {
		t.Errorf("morph weights = %v, want one zero per target", weights)
	}

	hairAttributes := document["meshes"].([]any)[1].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	if _, ok := hairAttributes["TEXCOORD_0"]; ok {
		t.Error("a part with no UVs should not declare TEXCOORD_0")
	}
}

func TestWriteSceneMaterials(t *testing.T) {
	_, _, document := buildScene(t)
	materials := document["materials"].([]any)
	head := materials[0].(map[string]any)
	if head["alphaMode"] != "MASK" || head["alphaCutoff"] != 0.5 {
		t.Errorf("head material = %v", head)
	}
	headPBR := head["pbrMetallicRoughness"].(map[string]any)
	if headPBR["roughnessFactor"] != 0.55 || headPBR["metallicFactor"] != float64(0) {
		t.Errorf("head PBR = %v", headPBR)
	}
	if _, ok := headPBR["baseColorTexture"]; !ok {
		t.Error("the textured part should reference a texture")
	}

	hairPBR := materials[1].(map[string]any)["pbrMetallicRoughness"].(map[string]any)
	if _, ok := hairPBR["baseColorTexture"]; ok {
		t.Error("the untextured part should have no texture")
	}
	factor := hairPBR["baseColorFactor"].([]any)
	if len(factor) != 4 || factor[0] != 0.035 {
		t.Errorf("hair tint = %v", factor)
	}
	if _, ok := materials[1].(map[string]any)["alphaMode"]; ok {
		t.Error("a part with no alpha flags should not declare alphaMode")
	}

	// The cache tag is stripped from the embedded image's name.
	images := document["images"].([]any)
	if got := images[0].(map[string]any)["name"]; got != "femalehead.png" {
		t.Errorf("image name = %v", got)
	}
}

func TestWriteSceneFailsOnMissingImage(t *testing.T) {
	target := scene(t, filepath.Join(t.TempDir(), "absent.png"))
	if _, _, err := write(target); err == nil {
		t.Fatal("a missing PNG should be an error")
	}
}

func TestImageNameStripsCacheTag(t *testing.T) {
	cases := map[string]string{
		"/cache/femalebody_1.0a1b2c3d.png": "femalebody_1.png",
		"/cache/LydiaHeadHP.png":           "LydiaHeadHP.png",
	}
	for path, want := range cases {
		if got := (Material{PNG: path}).ImageName(); got != want {
			t.Errorf("ImageName(%q) = %q, want %q", path, got, want)
		}
	}
}
