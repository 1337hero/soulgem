package glb

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"soulgem/internal/character"
	"soulgem/internal/mathutil"
	"soulgem/internal/nif"
	"soulgem/internal/tri"
)

// Scene is the typed model that sits between the game's assets and the glTF
// writer: a resolved skeleton, a list of drawable parts, and nothing that still
// needs a file opened or an index looked up.
type Scene struct {
	Name string
	// Joints are in topological order, so a joint's parent always precedes it.
	Joints []Joint
	Roots  []int
	Parts  []Part
}

type Joint struct {
	Name        string
	Children    []int
	Translation mathutil.Vec3
	Rotation    [4]float64
	Scale       float64
	// InverseBind is the joint's inverse bind matrix, flattened column-major
	// the way glTF wants it.
	InverseBind [16]float64
}

type Part struct {
	// Source is the mesh file this part came from, for reporting only.
	Source    string
	Name      string
	Positions []mathutil.Vec3
	Normals   []mathutil.Vec3
	UVs       [][2]float64
	Triangles [][3]uint16
	Joints    [][4]uint16
	Weights   [][4]float64
	Material  Material
	Morphs    []Morph
}

type Material struct {
	Name string
	// BaseColorFactor is nil unless the part is tinted rather than textured.
	BaseColorFactor []float64
	// PNG is the path to the embedded image, empty when the part is untextured.
	PNG string
	// Texture is the resolved DDS this PNG came from, for reporting only.
	Texture string
	// AlphaMode is "", "BLEND" or "MASK"; AlphaCutoff only applies to MASK.
	AlphaMode   string
	AlphaCutoff float64
}

type Morph struct {
	Name   string
	Deltas []mathutil.Vec3
}

const defaultSkeleton = "meshes/actors/character/character assets female/skeleton_female.nif"

// Resolve loads a character's assets and turns them into a Scene.
func Resolve(assets *Assets, cfg character.Config) (*Scene, error) {
	skeletonPath := cfg.Skeleton
	if skeletonPath == "" {
		skeletonPath = defaultSkeleton
	}
	skeletonNIF, err := nif.Open(resolve(cfg.Data, skeletonPath), nil)
	if err != nil {
		return nil, err
	}
	scene := &Scene{Name: cfg.Name}
	jointIndex, err := scene.addJoints(skeletonNIF)
	if err != nil {
		return nil, err
	}
	parts, err := loadParts(cfg, skeletonNIF.NodeGlobals)
	if err != nil {
		return nil, err
	}
	smoothNormals(parts)
	for i := range parts {
		if err := parts[i].finish(assets, cfg, jointIndex); err != nil {
			return nil, err
		}
		scene.Parts = append(scene.Parts, parts[i].Part)
	}
	return scene, nil
}

// addJoints flattens the skeleton into topological order: a joint is emitted
// once its parent has been, so the glTF node list never forward-references.
func (s *Scene) addJoints(skeleton *nif.File) (map[string]int, error) {
	jointIndex := make(map[string]int, len(skeleton.NodeOrder))
	remaining := make(map[string]bool, len(skeleton.NodeOrder))
	for _, name := range skeleton.NodeOrder {
		remaining[name] = true
	}
	for len(remaining) != 0 {
		progress := false
		for _, name := range skeleton.NodeOrder {
			if !remaining[name] {
				continue
			}
			node := skeleton.NodeTree[name]
			if node.Parent != "" {
				if _, ok := jointIndex[node.Parent]; !ok {
					continue
				}
			}
			jointIndex[name] = len(s.Joints)
			s.Joints = append(s.Joints, Joint{
				Name:        name,
				Translation: node.Transform.Translation,
				Rotation:    mathutil.QuaternionFromMatrix(node.Transform.Rotation),
				Scale:       node.Transform.Scale,
				InverseBind: columnMajor(mathutil.AffineInverse(skeleton.NodeGlobals[name])),
			})
			delete(remaining, name)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("skeleton contains an unresolved parent cycle")
		}
	}
	for _, joint := range s.Joints {
		parent := skeleton.NodeTree[joint.Name].Parent
		if parent == "" {
			s.Roots = append(s.Roots, jointIndex[joint.Name])
			continue
		}
		s.Joints[jointIndex[parent]].Children = append(
			s.Joints[jointIndex[parent]].Children, jointIndex[joint.Name])
	}
	return jointIndex, nil
}

func columnMajor(m mathutil.Mat4) [16]float64 {
	var out [16]float64
	for column := range 4 {
		for row := range 4 {
			out[column*4+row] = m[row][column]
		}
	}
	return out
}

// loading pairs a Part with the NIF shape it came from, which the later stages
// still need for skin weights and shader data.
type loading struct {
	Part
	shape   *nif.Shape
	options character.MeshOptions
}

// geometryKey identifies duplicate geometry. Outfit meshes ship several
// variants of the same body under different files; the first one wins.
type geometryKey struct {
	vertices, triangles int
	texture             string
}

func loadParts(cfg character.Config, skeleton map[string]mathutil.Mat4) ([]loading, error) {
	var parts []loading
	seen := make(map[geometryKey]bool)
	for _, mesh := range cfg.Meshes {
		model, err := nif.Open(resolve(cfg.Data, mesh.Path), skeleton)
		if err != nil {
			return nil, err
		}
		for _, shape := range model.Shapes {
			if len(shape.Positions) == 0 || len(shape.Triangles) == 0 || mesh.Options.Skip[shape.Name] {
				continue
			}
			texture := ""
			if len(shape.Textures) != 0 {
				texture = shape.Textures[0]
			}
			key := geometryKey{len(shape.Positions), len(shape.Triangles), texture}
			if seen[key] {
				continue
			}
			seen[key] = true
			parts = append(parts, loading{
				Part: Part{
					Source:    mesh.Path,
					Name:      shape.Name,
					Positions: worldPositions(shape),
					UVs:       shape.UVs,
					Triangles: shape.Triangles,
				},
				shape:   shape,
				options: mesh.Options,
			})
		}
	}
	return parts, nil
}

// worldPositions applies a shape's own transform. Skinned shapes have already
// been baked into bind pose and carry no transform.
func worldPositions(shape *nif.Shape) []mathutil.Vec3 {
	if shape.Transform == nil {
		return shape.Positions
	}
	out := make([]mathutil.Vec3, len(shape.Positions))
	for i, position := range shape.Positions {
		out[i] = mathutil.TransformPosition(*shape.Transform, position)
	}
	return out
}

// smoothNormals recomputes normals across the whole body at once, welding
// coincident vertices so seams between separate meshes disappear. Hair and eyes
// keep their authored normals: they are meant to look separate.
func smoothNormals(parts []loading) {
	var positions []mathutil.Vec3
	var triangles [][3]int
	type span struct{ part, base, count int }
	var spans []span
	for i := range parts {
		part := &parts[i]
		lower := strings.ToLower(part.Name)
		if len(part.shape.Normals) != 0 &&
			(strings.Contains(lower, "hair") || strings.Contains(lower, "eyes")) {
			part.Normals = part.shape.Normals
			continue
		}
		base := len(positions)
		positions = append(positions, part.Positions...)
		for _, triangle := range part.Triangles {
			triangles = append(triangles, [3]int{
				int(triangle[0]) + base, int(triangle[1]) + base, int(triangle[2]) + base,
			})
		}
		spans = append(spans, span{i, base, len(part.Positions)})
	}
	smoothed := mathutil.SmoothNormals(positions, triangles)
	for _, span := range spans {
		parts[span.part].Normals = smoothed[span.base : span.base+span.count]
	}
}

// finish fills in the parts of a Part that need the skeleton or the texture
// cache: skin bindings, the material, and the head's morph targets.
func (l *loading) finish(assets *Assets, cfg character.Config, jointIndex map[string]int) error {
	l.bindSkin(jointIndex)
	if err := l.resolveMaterial(assets, cfg); err != nil {
		return err
	}
	if l.Name != cfg.HeadShape {
		return nil
	}
	return l.loadMorphs(assets, cfg)
}

// bindSkin reduces each vertex to the four heaviest bones glTF allows, dropping
// bones the skeleton does not have and renormalising what is left.
func (l *loading) bindSkin(jointIndex map[string]int) {
	for _, vertexWeights := range l.shape.SkinWeights {
		top := make([]nif.BoneWeight, 0, len(vertexWeights))
		for _, weight := range vertexWeights {
			if _, ok := jointIndex[weight.Bone]; ok && weight.Weight > 0 {
				top = append(top, weight)
			}
		}
		// Heaviest first, ties broken by reverse bone name, which is what the
		// original exporter's sort key produced.
		slices.SortFunc(top, func(a, b nif.BoneWeight) int {
			switch {
			case a.Weight < b.Weight:
				return 1
			case a.Weight > b.Weight:
				return -1
			default:
				return strings.Compare(b.Bone, a.Bone)
			}
		})
		if len(top) > 4 {
			top = top[:4]
		}
		total := 0.0
		for _, weight := range top {
			total += weight.Weight
		}
		if total == 0 {
			total = 1
		}
		var joints [4]uint16
		var weights [4]float64
		for i, weight := range top {
			joints[i] = uint16(jointIndex[weight.Bone])
			weights[i] = weight.Weight / total
		}
		l.Joints = append(l.Joints, joints)
		l.Weights = append(l.Weights, weights)
	}
}

var cacheTag = regexp.MustCompile(`\.[0-9a-f]{8}\.png$`)

// ImageName is the name the embedded PNG carries in the glTF, with the cache
// tag stripped back off.
func (m Material) ImageName() string {
	return cacheTag.ReplaceAllString(filepath.Base(m.PNG), ".png")
}

func (l *loading) resolveMaterial(assets *Assets, cfg character.Config) error {
	shape := l.shape
	material := Material{Name: shape.Name}
	tint := l.options.Tint
	if tint == nil && strings.Contains(strings.ToLower(shape.Name), "hair") {
		tint = cfg.HairTint
	}
	if tint != nil {
		material.BaseColorFactor = []float64{(*tint)[0], (*tint)[1], (*tint)[2], 1}
	}
	if len(shape.Textures) != 0 {
		diffuse, err := assets.FindTexture(cfg, shape.Textures[0])
		if err != nil {
			return err
		}
		material.Texture = diffuse
	}
	switch {
	case material.Texture != "":
		png, err := assets.ToPNG(cfg, material.Texture, 1024)
		if err != nil {
			return err
		}
		material.PNG = png
	case strings.Contains(strings.ToLower(shape.Name), "mouth"):
		// The vanilla mouth has no diffuse of its own; without this it renders
		// as untinted white behind the lips.
		material.BaseColorFactor = []float64{0.23, 0.12, 0.10, 1}
	}
	if shape.AlphaFlags != nil {
		switch {
		case *shape.AlphaFlags&1 != 0:
			material.AlphaMode = "BLEND"
		case *shape.AlphaFlags&0x200 != 0:
			material.AlphaMode = "MASK"
			material.AlphaCutoff = float64(shape.AlphaThreshold) / 255
		}
	}
	l.Material = material
	return nil
}

// loadMorphs attaches the FaceGen visemes and expressions to the head, rotated
// out of the head bone's space into the mesh's.
func (l *loading) loadMorphs(assets *Assets, cfg character.Config) error {
	archive, err := assets.Archive(cfg.Data, cfg.HeadTriBSA)
	if err != nil {
		return err
	}
	data, err := archive.Read(cfg.HeadTri)
	if err != nil {
		return err
	}
	morphs, err := tri.Parse(data)
	if err != nil {
		return err
	}
	if morphs.NumVerts != len(l.Positions) {
		return fmt.Errorf("TRI vertices %d != head vertices %d", morphs.NumVerts, len(l.Positions))
	}
	headMatrix := l.shape.BoneMatrices["NPC Head [Head]"]
	for _, name := range cfg.MorphNames {
		deltas := morphs.Morphs[name]
		world := make([]mathutil.Vec3, len(deltas))
		for i, delta := range deltas {
			world[i] = mathutil.TransformDirection(headMatrix, delta)
		}
		l.Morphs = append(l.Morphs, Morph{Name: name, Deltas: world})
	}
	return nil
}

func resolve(data, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(data, filepath.FromSlash(path))
}
