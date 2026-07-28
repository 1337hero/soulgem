package nif

import (
	"soulgem/internal/binread"
	"soulgem/internal/mathutil"
)

// nodeTypes are the block types that make up the scene graph. BSFadeNode and
// BSFaceGenNiNodeSkinned are NiNode with extra fields we do not read.
var nodeTypes = map[string]bool{
	"NiNode": true, "BSFadeNode": true, "BSFaceGenNiNodeSkinned": true,
}

// readNamed reads the NiObjectNET prefix shared by every named block: name,
// extra-data list, controller.
func (f *File) readNamed(c *binread.Cursor) string {
	name := f.str(c, c.I32())
	c.Skip(4 * c.Count(int(c.U32()), 4, "extra data"))
	c.Skip(4) // controller
	return name
}

// readAVObject reads NiAVObject: the named prefix plus flags and the local
// transform.
func (f *File) readAVObject(c *binread.Cursor) (string, mathutil.Transform) {
	name := f.readNamed(c)
	c.Skip(4) // flags
	tf := readTransform(c)
	c.Skip(4) // collision object
	return name, tf
}

// vec3 reads three floats. A failed cursor yields zeros rather than a short
// slice, so callers can read a whole block and check the error once.
func vec3(c *binread.Cursor) mathutil.Vec3 {
	return mathutil.Vec3{c.F32(), c.F32(), c.F32()}
}

// mat3 reads a row-major 3x3 rotation.
func mat3(c *binread.Cursor) mathutil.Mat3 {
	var out mathutil.Mat3
	for i := range out {
		out[i] = c.F32()
	}
	return out
}

// readTransform reads a node's transform. Skin bones store the same three
// fields in a different order, so they are read separately in skin.go.
func readTransform(c *binread.Cursor) mathutil.Transform {
	translation := vec3(c)
	return mathutil.Transform{Translation: translation, Rotation: mat3(c), Scale: c.F32()}
}

// readNodes builds the scene graph: each node's local transform, its parent,
// and the accumulated global matrix the skinning readers need.
func (f *File) readNodes() error {
	for i, blockType := range f.BlockTypes {
		if !nodeTypes[blockType] {
			continue
		}
		c, err := f.block(i, "node")
		if err != nil {
			return err
		}
		name, tf := f.readAVObject(c)
		numChildren := c.Count(int(c.U32()), 4, "child")
		for range numChildren {
			if child := int(c.I32()); child >= 0 {
				f.parent[child] = i
			}
		}
		if err := c.Err(); err != nil {
			return err
		}
		f.nodeTF[i] = tf
		f.nodeName[i] = name
		f.NodeOrder = append(f.NodeOrder, name)
	}
	for i, tf := range f.nodeTF {
		f.NodeGlobals[f.nodeName[i]] = f.nodeGlobal(i)
		parent := ""
		if p, ok := f.parent[i]; ok {
			parent = f.nodeName[p]
		}
		f.NodeTree[f.nodeName[i]] = Node{Parent: parent, Transform: tf}
	}
	return nil
}

// nodeGlobal walks parents to the root, composing local transforms.
func (f *File) nodeGlobal(i int) mathutil.Mat4 {
	m := mathutil.TransformMatrix(f.nodeTF[i])
	for {
		p, ok := f.parent[i]
		if !ok {
			return m
		}
		i = p
		m = mathutil.Multiply(mathutil.TransformMatrix(f.nodeTF[i]), m)
	}
}

// chainTransform is nodeGlobal for a block that is not itself in nodeTF: the
// shape's own transform composed under its ancestors.
func (f *File) chainTransform(i int, own mathutil.Transform) mathutil.Mat4 {
	chain := []mathutil.Transform{own}
	for {
		p, ok := f.parent[i]
		if !ok {
			break
		}
		i = p
		chain = append(chain, f.nodeTF[i])
	}
	m := mathutil.TransformMatrix(chain[len(chain)-1])
	for j := len(chain) - 2; j >= 0; j-- {
		m = mathutil.Multiply(m, mathutil.TransformMatrix(chain[j]))
	}
	return m
}

// boneGlobal prefers the external skeleton's pose for a bone. A body mesh
// carries its own copy of the skeleton, and only the shared skeleton file has
// the poses every mesh must agree on.
func (f *File) boneGlobal(i int) mathutil.Mat4 {
	if f.Skeleton != nil {
		if m, ok := f.Skeleton[f.nodeName[i]]; ok {
			return m
		}
	}
	return f.nodeGlobal(i)
}
