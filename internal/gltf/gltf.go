// Package gltf writes binary glTF 2.0.
//
// The types here exist to make field order a property of the schema rather than
// of the code that fills it in. These files are regression-checked by hash
// against what Python's json.dumps produced, so both the order of keys and the
// spelling of every number are load-bearing; struct declaration order gives the
// first, and encode.go gives the second.
package gltf

// Document is the glTF JSON chunk. Field order here is the key order on disk.
type Document struct {
	Asset       Asset        `json:"asset"`
	Scene       int          `json:"scene"`
	Scenes      []Scene      `json:"scenes"`
	Skins       []Skin       `json:"skins"`
	Nodes       []Node       `json:"nodes"`
	Meshes      []Mesh       `json:"meshes"`
	Materials   []Material   `json:"materials"`
	Accessors   []Accessor   `json:"accessors"`
	BufferViews []BufferView `json:"bufferViews"`
	Samplers    []Sampler    `json:"samplers"`
	Images      []Image      `json:"images"`
	Textures    []Texture    `json:"textures"`
	Buffers     []Buffer     `json:"buffers"`
}

type Asset struct {
	Version   string `json:"version"`
	Generator string `json:"generator"`
}

type Scene struct {
	Nodes []int `json:"nodes"`
}

type Skin struct {
	Joints              []int `json:"joints"`
	InverseBindMatrices int   `json:"inverseBindMatrices"`
	Skeleton            int   `json:"skeleton"`
}

// Node covers all three shapes this pipeline emits — a skeleton joint, a mesh
// instance, and the scene root — in one declaration order that suits each.
type Node struct {
	Name        string   `json:"name"`
	Translation []Float  `json:"translation,omitempty"`
	Rotation    []Float  `json:"rotation,omitempty"`
	Scale       []Float  `json:"scale,omitempty"`
	Children    []int    `json:"children,omitempty"`
	Mesh        *int     `json:"mesh,omitempty"`
	Skin        *int     `json:"skin,omitempty"`
	Matrix      []Number `json:"matrix,omitempty"`
}

type Mesh struct {
	Name       string      `json:"name"`
	Primitives []Primitive `json:"primitives"`
	Weights    []Float     `json:"weights,omitempty"`
	Extras     *Extras     `json:"extras,omitempty"`
}

type Extras struct {
	TargetNames []string `json:"targetNames"`
}

type Primitive struct {
	Attributes Attributes    `json:"attributes"`
	Indices    int           `json:"indices"`
	Material   int           `json:"material"`
	Targets    []MorphTarget `json:"targets,omitempty"`
}

type Attributes struct {
	Position  int  `json:"POSITION"`
	Normal    int  `json:"NORMAL"`
	TexCoord0 *int `json:"TEXCOORD_0,omitempty"`
	Joints0   *int `json:"JOINTS_0,omitempty"`
	Weights0  *int `json:"WEIGHTS_0,omitempty"`
}

type MorphTarget struct {
	Position int `json:"POSITION"`
}

type Material struct {
	Name        string `json:"name"`
	PBR         PBR    `json:"pbrMetallicRoughness"`
	DoubleSided bool   `json:"doubleSided"`
	AlphaMode   string `json:"alphaMode,omitempty"`
	AlphaCutoff *Float `json:"alphaCutoff,omitempty"`
}

type PBR struct {
	MetallicFactor   Float       `json:"metallicFactor"`
	RoughnessFactor  Float       `json:"roughnessFactor"`
	BaseColorFactor  []Float     `json:"baseColorFactor,omitempty"`
	BaseColorTexture *TextureRef `json:"baseColorTexture,omitempty"`
}

type TextureRef struct {
	Index int `json:"index"`
}

type Accessor struct {
	BufferView    int     `json:"bufferView"`
	ComponentType int     `json:"componentType"`
	Count         int     `json:"count"`
	Type          string  `json:"type"`
	Min           []Float `json:"min,omitempty"`
	Max           []Float `json:"max,omitempty"`
}

type BufferView struct {
	Buffer     int `json:"buffer"`
	ByteOffset int `json:"byteOffset"`
	ByteLength int `json:"byteLength"`
	Target     int `json:"target,omitempty"`
}

type Sampler struct {
	MagFilter int `json:"magFilter"`
	MinFilter int `json:"minFilter"`
	WrapS     int `json:"wrapS"`
	WrapT     int `json:"wrapT"`
}

type Image struct {
	BufferView int    `json:"bufferView"`
	MimeType   string `json:"mimeType"`
	Name       string `json:"name"`
}

type Texture struct {
	Sampler int `json:"sampler"`
	Source  int `json:"source"`
}

type Buffer struct {
	ByteLength int `json:"byteLength"`
}

// glTF component and target constants, spelled out so the accessor helpers read
// as something other than magic numbers.
const (
	componentUnsignedShort = 5123
	componentFloat         = 5126

	targetArrayBuffer        = 34962
	targetElementArrayBuffer = 34963

	filterLinear             = 9729
	filterLinearMipmapLinear = 9987
	wrapRepeat               = 10497
)

// DefaultSampler is the only sampler these characters use: bilinear with
// mipmaps, repeating in both directions.
func DefaultSampler() Sampler {
	return Sampler{
		MagFilter: filterLinear, MinFilter: filterLinearMipmapLinear,
		WrapS: wrapRepeat, WrapT: wrapRepeat,
	}
}
