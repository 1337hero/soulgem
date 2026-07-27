"""Minimal Skyrim SE NIF parser — enough to extract BSTriShape geometry + textures."""
import struct

from mathutil import bake_bind_pose
from mathutil import mat_mul as _mat_mul
from mathutil import tf_mat as _tf_mat


class Reader:
    def __init__(self, data, off=0):
        self.d = data
        self.o = off

    def u8(self):  return self._one('<B', 1)
    def u16(self): return self._one('<H', 2)
    def u32(self): return self._one('<I', 4)
    def i32(self): return self._one('<i', 4)
    def u64(self): return self._one('<Q', 8)
    def f32(self): return self._one('<f', 4)

    def _one(self, fmt, n):
        v = struct.unpack_from(fmt, self.d, self.o)[0]
        self.o += n
        return v

    def f32s(self, n):
        v = struct.unpack_from('<%df' % n, self.d, self.o)
        self.o += 4 * n
        return v

    def u16s(self, n):
        v = struct.unpack_from('<%dH' % n, self.d, self.o)
        self.o += 2 * n
        return v

    def bytes(self, n):
        v = self.d[self.o:self.o + n]
        self.o += n
        return v

    def sized_string(self):
        return self.bytes(self.u32()).decode('latin-1')

    def short_string(self):  # byte-length-prefixed, null-terminated
        return self.bytes(self.u8()).decode('latin-1').rstrip('\x00')


def half(h):
    return struct.unpack('<e', struct.pack('<H', h))[0]


class Shape:
    __slots__ = ('name', 'positions', 'normals', 'uvs', 'colors', 'triangles',
                 'textures', 'alpha_flags', 'alpha_threshold', 'shader_type',
                 'transform', 'skinned', 'specular', 'glossiness', 'skin_weights',
                 'bone_mats')

    def __init__(self):
        self.name = ''
        self.positions = []
        self.normals = []
        self.uvs = []
        self.colors = []
        self.triangles = []
        self.textures = []
        self.alpha_flags = None
        self.alpha_threshold = 0
        self.shader_type = None
        self.transform = None  # (translation, 3x3 rotation, scale) accumulated
        self.skinned = False
        self.specular = (1, 1, 1)
        self.glossiness = 80.0
        self.skin_weights = []  # per-vertex [(bone_name, weight), ...]
        self.bone_mats = {}      # bone name -> bone-to-global bind matrix


def read_vertex_block(r, desc, num_verts):
    """Decode BSVertexDataSSE array.
    Returns (positions, uvs, normals, colors, weights, bone_indices)."""
    flags = (desc >> 44) & 0x7FF
    stride = (desc & 0xF) * 4
    has_pos = flags & 0x1
    has_uv = flags & 0x2
    has_nrm = flags & 0x8
    has_tan = (flags & 0x18) == 0x18
    has_col = flags & 0x20
    has_skin = flags & 0x40
    has_eye = flags & 0x100
    positions, uvs, normals, colors, weights, bindices = [], [], [], [], [], []
    for _ in range(num_verts):
        start = r.o
        if has_pos:
            positions.append(r.f32s(3))
            r.o += 4  # bitangent X or unused
        if has_uv:
            uvs.append((half(r.u16()), half(r.u16())))
        if has_nrm:
            n = r.bytes(4)  # normal xyz + bitangent Y
            normals.append(tuple(b / 127.5 - 1.0 for b in n[:3]))
        if has_tan:
            r.o += 4
        if has_col:
            colors.append(tuple(b / 255.0 for b in r.bytes(4)))
        if has_skin:
            weights.append(tuple(half(r.u16()) for _ in range(4)))
            bindices.append(tuple(r.bytes(4)))
        if has_eye:
            r.o += 4
        used = r.o - start
        assert used == stride, f'stride mismatch: computed {used}, declared {stride} (flags {flags:#x})'
    return positions, uvs, normals, colors, weights, bindices


class NifFile:
    def __init__(self, path, skeleton=None):
        self.skeleton = skeleton or {}
        data = open(path, 'rb').read()
        nl = data.index(b'\n')
        header_str = data[:nl].decode()
        assert '20.2.0.7' in header_str, header_str
        r = Reader(data, nl + 1)
        assert r.u32() == 0x14020007
        assert r.u8() == 1  # little endian
        self.user_version = r.u32()
        num_blocks = r.u32()
        self.stream = r.u32()  # BS stream: 100 = SSE
        r.short_string(); r.short_string(); r.short_string()  # export info
        num_types = r.u16()
        types = [r.sized_string() for _ in range(num_types)]
        type_index = [r.u16() & 0x7FFF for _ in range(num_blocks)]
        sizes = [r.u32() for _ in range(num_blocks)]
        num_strings = r.u32()
        r.u32()  # max string length
        self.strings = [r.sized_string() for _ in range(num_strings)]
        r.u32()  # num groups
        self.block_types = [types[i] for i in type_index]
        self.block_offsets = []
        o = r.o
        for s in sizes:
            self.block_offsets.append(o)
            o += s
        self.data = data
        self.shapes = []
        self._parse()

    def block_reader(self, i):
        return Reader(self.data, self.block_offsets[i])

    def _read_avobject(self, r, is_shader=False):
        """NiObjectNET + NiAVObject prefix. Returns (name, translation, rotation, scale, last_field)."""
        shader_type = r.u32() if is_shader else None
        name_idx = r.i32()
        name = self.strings[name_idx] if name_idx >= 0 else ''
        num_extra = r.u32()
        r.o += 4 * num_extra
        r.i32()  # controller
        if is_shader:
            return name, shader_type
        r.u32()  # flags (uint32 for BS stream > 26)
        translation = r.f32s(3)
        rotation = r.f32s(9)
        scale = r.f32()
        r.i32()  # collision object
        return name, translation, rotation, scale

    def _parse(self):
        # First pass: node children so we can accumulate parent transforms.
        parent = {}
        node_tf = {}
        node_name = {}
        for i, bt in enumerate(self.block_types):
            if bt in ('NiNode', 'BSFadeNode', 'BSFaceGenNiNodeSkinned'):
                r = self.block_reader(i)
                name, t, rot, s = self._read_avobject(r)
                node_tf[i] = (t, rot, s)
                node_name[i] = name
                num_children = r.u32()
                for _ in range(num_children):
                    c = r.i32()
                    if c >= 0:
                        parent[c] = i
        self._node_name = node_name
        self.node_globals = {node_name[i]: self._node_global(i, parent, node_tf)
                             for i in node_tf}
        self.node_tree = {node_name[i]: (node_name.get(parent.get(i)), node_tf[i])
                          for i in node_tf}

        for i, bt in enumerate(self.block_types):
            if bt not in ('BSTriShape', 'BSDynamicTriShape', 'NiTriShape'):
                continue
            r = self.block_reader(i)
            sh = Shape()
            sh.name, t, rot, s = self._read_avobject(r)
            sh.transform = self._chain_transform(i, parent, node_tf, (t, rot, s))
            if bt in ('BSTriShape', 'BSDynamicTriShape'):
                r.f32s(4)  # bounding sphere
                skin_ref = r.i32()
                shader_ref = r.i32()
                alpha_ref = r.i32()
                desc = r.u64()
                num_tris = r.u16()
                num_verts = r.u16()
                data_size = r.u32()
                weights = bindices = []
                if data_size > 0:
                    (sh.positions, sh.uvs, sh.normals, sh.colors,
                     weights, bindices) = read_vertex_block(r, desc, num_verts)
                    for _ in range(num_tris):
                        sh.triangles.append(r.u16s(3))
                if bt == 'BSDynamicTriShape':
                    dyn_size = r.u32()
                    if dyn_size == 0:  # was particle data size; real size follows
                        dyn_size = r.u32()
                    assert dyn_size == num_verts * 16, (dyn_size, num_verts)
                    sh.positions = [r.f32s(4)[:3] for _ in range(num_verts)]
                if skin_ref >= 0:
                    sh.skinned = True
                    self._read_skin(skin_ref, sh, parent, node_tf,
                                    weights, bindices)
            else:  # NiTriShape (LE / stream 83)
                data_ref = r.i32()
                skin_ref = r.i32()
                num_materials = r.u32()
                r.o += 8 * num_materials  # name idx + extra data per material
                r.i32()  # active material
                r.u8()   # material needs update
                shader_ref = r.i32()
                alpha_ref = r.i32()
                sh.skinned = skin_ref >= 0
                if data_ref >= 0:
                    self._read_trishape_data(data_ref, sh)
                if skin_ref >= 0:
                    self._apply_le_skinning(skin_ref, sh, parent, node_tf)
            if shader_ref >= 0:
                self._read_shader(shader_ref, sh)
            # NiAlphaProperty: NiObjectNET (name/extra/controller) + flags u16 + threshold u8
            if alpha_ref >= 0:
                ar = self.block_reader(alpha_ref)
                name_idx = ar.i32()
                ne = ar.u32(); ar.o += 4 * ne
                ar.i32()
                sh.alpha_flags = ar.u16()
                sh.alpha_threshold = ar.u8()
            self.shapes.append(sh)

    def _chain_transform(self, i, parent, node_tf, own):
        chain = [own]
        j = i
        while j in parent:
            j = parent[j]
            chain.append(node_tf[j])
        chain.reverse()
        m = _tf_mat(chain[0])
        for tf in chain[1:]:
            m = _mat_mul(m, _tf_mat(tf))
        return m

    def _read_trishape_data(self, ref, sh):
        assert self.block_types[ref] == 'NiTriShapeData', self.block_types[ref]
        r = self.block_reader(ref)
        r.i32()  # group id
        num_verts = r.u16()
        r.u8(); r.u8()  # keep flags, compress flags
        if r.u8():  # has vertices
            for _ in range(num_verts):
                sh.positions.append(r.f32s(3))
        vector_flags = r.u16()
        num_uv_sets = vector_flags & 0x1
        r.u32()  # material CRC (BS 20.2.0.7)
        if r.u8():  # has normals
            for _ in range(num_verts):
                sh.normals.append(r.f32s(3))
            if vector_flags & 0x1000:  # tangents + bitangents
                r.o += 24 * num_verts
        r.f32s(4)  # center + radius
        if r.u8():  # has vertex colors
            for _ in range(num_verts):
                sh.colors.append(r.f32s(4))
        for s in range(num_uv_sets):
            uvs = [(r.f32(), r.f32()) for _ in range(num_verts)]
            if s == 0:
                sh.uvs = uvs
        r.u16()  # consistency flags
        r.i32()  # additional data
        num_tris = r.u16()
        r.u32()  # num triangle points
        if r.u8():  # has triangles
            for _ in range(num_tris):
                sh.triangles.append(r.u16s(3))

    def _node_global(self, i, parent, node_tf):
        m = _tf_mat(node_tf[i])
        j = i
        while j in parent:
            j = parent[j]
            m = _mat_mul(_tf_mat(node_tf[j]), m)
        return m

    def _bone_global(self, i, parent, node_tf):
        name = self._node_name.get(i)
        if name in self.skeleton:
            return self.skeleton[name]
        return self._node_global(i, parent, node_tf)

    def _apply_le_skinning(self, skin_ref, sh, parent, node_tf):
        """Transform vertices to global bind pose via NiSkinData bone weights."""
        r = self.block_reader(skin_ref)
        data_ref = r.i32()
        r.i32()  # skin partition
        r.i32()  # skeleton root (ptr)
        num_bones = r.u32()
        bone_refs = [r.i32() for _ in range(num_bones)]
        if data_ref < 0:
            return
        bone_globals = [self._bone_global(b, parent, node_tf) for b in bone_refs]

        dr = self.block_reader(data_ref)
        assert self.block_types[data_ref] == 'NiSkinData'
        dr.f32s(9); dr.f32s(3); dr.f32()  # overall skin transform
        nb = dr.u32()
        assert nb == num_bones, (nb, num_bones)
        has_weights = dr.u8()
        influences = [[] for _ in sh.positions]
        sh.skin_weights = [[] for _ in sh.positions]
        for b in range(num_bones):
            rot = dr.f32s(9)
            trans = dr.f32s(3)
            scale = dr.f32()
            dr.f32s(4)  # bounding sphere
            bone_to_global = _mat_mul(bone_globals[b], _tf_mat((trans, rot, scale)))
            sh.bone_mats[self._node_name.get(bone_refs[b], '')] = bone_to_global
            nv = dr.u16()
            if not has_weights:
                continue
            bone_name = self._node_name.get(bone_refs[b], '')
            for _ in range(nv):
                vi = dr.u16()
                w = dr.f32()
                sh.skin_weights[vi].append((bone_name, w))
                influences[vi].append((bone_to_global, w))
        if has_weights:
            sh.positions, sh.normals = bake_bind_pose(sh.positions, sh.normals, influences)
            sh.transform = None  # already global

    def _read_skin(self, skin_ref, sh, parent, node_tf, weights, bindices):
        r = self.block_reader(skin_ref)
        bt = self.block_types[skin_ref]
        assert bt in ('NiSkinInstance', 'BSDismemberSkinInstance'), bt
        data_ref = r.i32()
        part_ref = r.i32()
        r.i32()  # skeleton root ptr
        num_bones = r.u32()
        bone_refs = [r.i32() for _ in range(num_bones)]
        if part_ref < 0:
            return
        pr = self.block_reader(part_ref)
        num_partitions = pr.u32()
        data_size = pr.u32()
        vertex_size = pr.u32()
        desc = pr.u64()
        if data_size > 0:
            num_verts = data_size // vertex_size
            pos2, uvs2, nrm2, col2, w2, b2 = read_vertex_block(pr, desc, num_verts)
            if pos2:
                sh.positions = pos2
            sh.uvs = uvs2 or sh.uvs
            sh.normals = nrm2 or sh.normals
            sh.colors = col2 or sh.colors
            weights, bindices = w2 or weights, b2 or bindices
        partitions = []  # (bones, vertex_map or None, triangles)
        for _ in range(num_partitions):
            nv = pr.u16()
            nt = pr.u16()
            nb = pr.u16()
            ns = pr.u16()
            wpv = pr.u16()
            pbones = list(pr.u16s(nb))
            vmap = None
            if pr.u8():  # has vertex map
                vmap = list(pr.u16s(nv))
            if pr.u8():  # has vertex weights
                pr.o += 4 * nv * wpv
            strip_lengths = pr.u16s(ns)
            has_faces = pr.u8()
            ptris = []
            if has_faces:
                if ns:
                    pr.o += 2 * sum(strip_lengths)
                else:
                    for _ in range(nt):
                        ptris.append(pr.u16s(3))
            if pr.u8():  # has bone indices
                pr.o += nv * wpv
            pr.o += 1  # LOD level
            pr.o += 1  # global VB
            pr.u64()  # vertex desc
            tris_copy = [pr.u16s(3) for _ in range(nt)]
            tris = tris_copy or ptris
            sh.triangles.extend(tris)
            partitions.append((pbones, vmap, tris))

        if not weights or data_ref < 0:
            return
        # NiSkinData: global skin transform + per-bone skin-to-bone transforms
        dr = self.block_reader(data_ref)
        assert self.block_types[data_ref] == 'NiSkinData'
        dr.f32s(13)  # overall skin transform
        nb = dr.u32()
        has_w = dr.u8()
        bone_mats = []
        for b in range(nb):
            rot = dr.f32s(9)
            trans = dr.f32s(3)
            scale = dr.f32()
            dr.f32s(4)  # bounding sphere
            g = self._bone_global(bone_refs[b], parent, node_tf)
            bone_mats.append(_mat_mul(g, _tf_mat((trans, rot, scale))))
            nv = dr.u16()
            if has_w:
                dr.o += 6 * nv
        # vertex -> partition bone list (via vertex map, else triangle refs)
        vert_part = {}
        for pi, (pbones, vmap, tris) in enumerate(partitions):
            verts = vmap if vmap else {i for t in tris for i in t}
            for v in verts:
                vert_part.setdefault(v, pi)
        influences = [[] for _ in sh.positions]
        sh.skin_weights = [[] for _ in sh.positions]
        for vi in range(len(sh.positions)):
            pbones = partitions[vert_part.get(vi, 0)][0]
            for w, bi in zip(weights[vi], bindices[vi]):
                if w == 0.0:
                    continue
                gbi = pbones[bi] if bi < len(pbones) else bi
                sh.skin_weights[vi].append((self._node_name.get(bone_refs[gbi], ''), w))
                influences[vi].append((bone_mats[gbi], w))
        sh.positions, sh.normals = bake_bind_pose(sh.positions, sh.normals, influences)
        sh.transform = None  # already global

    def _read_shader(self, ref, sh):
        bt = self.block_types[ref]
        if bt != 'BSLightingShaderProperty':
            sh.shader_type = bt
            return
        r = self.block_reader(ref)
        _, sh.shader_type = self._read_avobject(r, is_shader=True)
        r.u32(); r.u32()  # shader flags 1, 2
        r.f32s(2); r.f32s(2)  # uv offset, uv scale
        tex_ref = r.i32()
        r.f32s(3); r.f32()  # emissive color, mult
        r.u32()  # texture clamp
        r.f32()  # alpha
        r.f32()  # refraction
        sh.glossiness = r.f32()
        sh.specular = r.f32s(3)
        if tex_ref >= 0:
            assert self.block_types[tex_ref] == 'BSShaderTextureSet', self.block_types[tex_ref]
            tr = self.block_reader(tex_ref)
            n = tr.u32()
            sh.textures = [tr.sized_string() for _ in range(n)]


if __name__ == '__main__':
    import sys
    for path in sys.argv[1:]:
        nif = NifFile(path)
        print(f'== {path.split("/")[-1]}  (stream {nif.stream}, blocks: {len(nif.block_types)})')
        for sh in nif.shapes:
            zs = [p[2] for p in sh.positions] or [0]
            print(f'  shape "{sh.name}" verts={len(sh.positions)} tris={len(sh.triangles)} '
                  f'skinned={sh.skinned} shaderType={sh.shader_type} alpha={sh.alpha_flags} '
                  f'z=[{min(zs):.1f},{max(zs):.1f}] uv={len(sh.uvs) > 0} col={len(sh.colors) > 0}')
            for t in sh.textures:
                if t:
                    print(f'      tex: {t}')
