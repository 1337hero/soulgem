package nif

import "soulgem/internal/mathutil"

// readShader reads BSLightingShaderProperty, the only shader carrying the
// texture set and specular values the exporter uses. Anything else is recorded
// by name so callers can see what a shape actually had.
func (f *File) readShader(ref int, shape *Shape) error {
	c, err := f.block(ref, "shader property")
	if err != nil {
		return err
	}
	if f.BlockTypes[ref] != "BSLightingShaderProperty" {
		shape.ShaderType = f.BlockTypes[ref]
		return nil
	}
	shaderType := c.U32()
	f.readNamed(c)
	c.Skip(8)  // shader flags
	c.Skip(16) // UV offset and scale
	textureRef := int(c.I32())
	c.Skip(12) // emissive colour
	c.Skip(4)  // emissive multiple
	c.Skip(4)  // texture clamp mode
	c.Skip(4)  // alpha
	c.Skip(4)  // refraction strength
	glossiness := c.F32()
	specular := c.F32s(3)
	if err := c.Err(); err != nil {
		return err
	}
	shape.ShaderType = shaderType
	shape.Glossiness = glossiness
	shape.Specular = mathutil.Vec3{specular[0], specular[1], specular[2]}
	if textureRef < 0 {
		return nil
	}
	textures, err := f.typedBlock(textureRef, "BSShaderTextureSet", "texture set")
	if err != nil {
		return err
	}
	for range textures.Count(int(textures.U32()), 4, "texture") {
		shape.Textures = append(shape.Textures, textures.SizedString())
	}
	return textures.Err()
}
