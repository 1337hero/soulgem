"""FaceGen TRI (FRTRI003) morph file parser — Skyrim expression/phoneme morphs."""
import struct


class TriFile:
    def __init__(self, data):
        assert data[:8] == b'FRTRI003', data[:8]
        (self.num_verts, num_tris, num_quads, unk1, unk2, num_uv, flags,
         num_morphs, num_mods, num_mod_verts) = struct.unpack_from('<10i', data, 8)
        o = 64
        self.verts = struct.unpack_from('<%df' % (self.num_verts * 3), data, o)
        o += self.num_verts * 12
        o += num_mod_verts * 12
        o += num_tris * 12  # triangle indices
        if flags & 1:
            o += num_uv * 8       # uv coords
            o += num_tris * 12    # uv indices
        self.morphs = {}  # name -> list of (dx,dy,dz) per vertex
        for _ in range(num_morphs):
            nlen = struct.unpack_from('<I', data, o)[0]
            o += 4
            name = data[o:o + nlen].rstrip(b'\x00').decode('latin-1')
            o += nlen
            scale = struct.unpack_from('<f', data, o)[0]
            o += 4
            raw = struct.unpack_from('<%dh' % (self.num_verts * 3), data, o)
            o += self.num_verts * 6
            self.morphs[name] = [(raw[i] * scale, raw[i+1] * scale, raw[i+2] * scale)
                                 for i in range(0, len(raw), 3)]
        self.trailing = len(data) - o


if __name__ == '__main__':
    import sys
    from bsa import BSA
    if sys.argv[1].endswith('.bsa'):
        data = BSA(sys.argv[1]).read(sys.argv[2])
    else:
        data = open(sys.argv[1], 'rb').read()
    t = TriFile(data)
    print(f'verts={t.num_verts} morphs={len(t.morphs)} trailing_bytes={t.trailing}')
    for name, deltas in t.morphs.items():
        mx = max(abs(c) for v in deltas for c in v)
        print(f'  {name}: max_delta={mx:.3f}')
