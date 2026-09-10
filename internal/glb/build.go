// Package glb assembles Soulgem characters from NIF geometry and embedded PNG
// textures.
//
// It runs in three stages: assets.go resolves and converts textures, scene.go
// turns the game's meshes into a typed Scene, and Build walks that Scene into
// the glTF writer. Build returns bytes and a report; writing files and printing
// progress belong to the command.
package glb

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"soulgem/internal/character"
	"soulgem/internal/gltf"
)

// gameToGLTF converts the game's units and axes: centimetre-ish units down to
// metres, Z-up left-handed to Y-up right-handed.
const gameToGLTF = 0.01428

// Report describes what a build produced, for the command to print.
type Report struct {
	Parts    []PartReport
	Size     int
	Meshes   int
	Textures int
}

type PartReport struct {
	Source       string
	Name         string
	Vertices     int
	Texture      string
	MorphTargets int
}

// Build resolves a character and serialises it as a GLB.
func Build(assets *Assets, cfg character.Config) ([]byte, Report, error) {
	scene, err := Resolve(assets, cfg)
	if err != nil {
		return nil, Report{}, err
	}
	return write(scene)
}

func write(scene *Scene) ([]byte, Report, error) {
	writer := gltf.NewWriter("lydia nif2gltf")
	for _, joint := range scene.Joints {
		writer.AddNode(gltf.Node{
			Name:        joint.Name,
			Translation: gltf.Floats(joint.Translation[:]...),
			Rotation:    gltf.Floats(joint.Rotation[:]...),
			Scale:       gltf.Floats(joint.Scale, joint.Scale, joint.Scale),
			Children:    joint.Children,
		})
	}

	var report Report
	var meshNodes []int
	for _, part := range scene.Parts {
		meshNode, entry, err := writePart(writer, part)
		if err != nil {
			return nil, Report{}, err
		}
		meshNodes = append(meshNodes, meshNode)
		report.Parts = append(report.Parts, entry)
	}

	rootNode := writer.AddNode(gltf.Node{
		Name:     scene.Name,
		Children: scene.Roots,
		Matrix:   axisMatrix(gameToGLTF),
	})

	inverseBind := make([][16]float64, len(scene.Joints))
	joints := make([]int, len(scene.Joints))
	for i, joint := range scene.Joints {
		inverseBind[i] = joint.InverseBind
		joints[i] = i
	}
	writer.Document.Skins = []gltf.Skin{{
		Joints:              joints,
		InverseBindMatrices: writer.AddMatrices(inverseBind),
		Skeleton:            scene.Roots[0],
	}}
	writer.Document.Scenes = []gltf.Scene{{Nodes: append([]int{rootNode}, meshNodes...)}}

	data, err := writer.Bytes()
	if err != nil {
		return nil, Report{}, err
	}
	report.Size = len(data)
	report.Meshes = len(writer.Document.Meshes)
	report.Textures = len(writer.Document.Images)
	return data, report, nil
}

// writePart adds one part's accessors, material, mesh and node, in the order
// the binary chunk's layout depends on.
func writePart(writer *gltf.Writer, part Part) (int, PartReport, error) {
	attributes := gltf.Attributes{
		Position: writer.AddPositions(part.Positions),
		Normal:   writer.AddVec3(part.Normals),
	}
	if len(part.UVs) != 0 {
		attributes.TexCoord0 = pointer(writer.AddVec2(part.UVs))
	}
	if len(part.Joints) != 0 {
		attributes.Joints0 = pointer(writer.AddJoints(part.Joints))
		attributes.Weights0 = pointer(writer.AddVec4(part.Weights))
	}
	indices := writer.AddTriangles(part.Triangles)

	material, err := writeMaterial(writer, part.Material)
	if err != nil {
		return 0, PartReport{}, err
	}
	primitive := gltf.Primitive{
		Attributes: attributes,
		Indices:    indices,
		Material:   writer.AddMaterial(material),
	}
	mesh := gltf.Mesh{Name: part.Name}
	if len(part.Morphs) != 0 {
		names := make([]string, len(part.Morphs))
		for i, morph := range part.Morphs {
			primitive.Targets = append(primitive.Targets,
				gltf.MorphTarget{Position: writer.AddPositions(morph.Deltas)})
			names[i] = morph.Name
		}
		mesh.Weights = make([]gltf.Float, len(part.Morphs))
		mesh.Extras = &gltf.Extras{TargetNames: names}
	}
	mesh.Primitives = []gltf.Primitive{primitive}

	node := gltf.Node{Name: part.Name, Mesh: pointer(writer.AddMesh(mesh))}
	if attributes.Joints0 != nil {
		node.Skin = pointer(0)
	}
	return writer.AddNode(node), PartReport{
		Source:       filepath.Base(part.Source),
		Name:         part.Name,
		Vertices:     len(part.Positions),
		Texture:      textureName(part.Material.Texture),
		MorphTargets: len(part.Morphs),
	}, nil
}

func writeMaterial(writer *gltf.Writer, material Material) (gltf.Material, error) {
	out := gltf.Material{
		Name:        material.Name,
		PBR:         gltf.PBR{MetallicFactor: 0, RoughnessFactor: 0.55},
		DoubleSided: true,
		AlphaMode:   material.AlphaMode,
	}
	out.PBR.BaseColorFactor = gltf.Floats(material.BaseColorFactor...)
	if material.AlphaMode == "MASK" {
		out.AlphaCutoff = pointer(gltf.Float(material.AlphaCutoff))
	}
	if material.PNG != "" {
		png, err := os.ReadFile(material.PNG)
		if err != nil {
			return gltf.Material{}, err
		}
		index := writer.AddImage(material.ImageName(), png, material.PNG)
		out.PBR.BaseColorTexture = &gltf.TextureRef{Index: index}
	}
	return out, nil
}

// axisMatrix is the root node's transform. Python built it from a mix of int
// and float literals and the output is hashed, so the spelling is preserved.
func axisMatrix(scale float64) []gltf.Number {
	zero, one := gltf.Int(0), gltf.Int(1)
	return []gltf.Number{
		gltf.Decimal(-scale), zero, zero, zero,
		zero, zero, gltf.Decimal(scale), zero,
		zero, gltf.Decimal(scale), zero, zero,
		zero, zero, zero, one,
	}
}

func textureName(path string) string {
	if path == "" {
		return "None"
	}
	return filepath.Base(path)
}

func pointer[T any](value T) *T { return &value }

func SHA(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
