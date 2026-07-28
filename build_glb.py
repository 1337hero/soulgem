"""Assemble a character: parse NIFs -> single GLB with embedded PNG textures.

One character = one module under characters/; main(cfg) does the rest. Characters
must not be able to affect each other: nothing here reads a global default that a
new character could change, so every optional capability is off unless that
character's own config turns it on. `--verify` proves it.
"""
import argparse
import hashlib
import importlib
import json
import os
import re
import struct
import subprocess

from bsa import BSA
from mathutil import affine_inverse, quat_from_mat3, smooth_normals
from nif import NifFile
from tri import TriFile

DATA = '/home/mikekey/.local/share/Steam/steamapps/common/Skyrim Special Edition/Data'
ROOT = os.path.dirname(os.path.abspath(__file__))
CHARACTERS = os.path.join(ROOT, 'characters')
CACHE = os.path.join(os.path.dirname(__file__), 'texcache')
os.makedirs(CACHE, exist_ok=True)
TEXTURE_BSAS = [f'Skyrim - Textures{i}.bsa' for i in range(9)]
MESH_BSAS = ['Skyrim - Meshes0.bsa', 'Skyrim - Meshes1.bsa']

# vanilla femalehead.tri morphs, in TRI order — same for every human female head
MORPH_NAMES = [
    # visemes
    'Aah', 'BigAah', 'BMP', 'ChJSh', 'DST', 'Eee', 'Eh', 'FV', 'I', 'K', 'N',
    'Oh', 'OohQ', 'R', 'Th', 'W',
    # eyes / brows
    'BlinkLeft', 'BlinkRight', 'SquintLeft', 'SquintRight', 'LookDown',
    'BrowDownLeft', 'BrowDownRight', 'BrowInLeft', 'BrowInRight',
    'BrowUpLeft', 'BrowUpRight',
    # moods
    'MoodHappy', 'MoodSad', 'MoodAnger', 'MoodFear', 'MoodSurprise',
    'MoodPuzzled', 'MoodDisgusted',
]
HEAD_TRI = ('Skyrim - Meshes0.bsa', 'meshes/actors/character/character assets/femalehead.tri')


def _ci_find(root, rel):
    # exact path first, then a case-insensitive walk component by component
    p = os.path.join(root, rel)
    if os.path.isfile(p):
        return p
    cur = root
    for part in rel.split('/'):
        if not os.path.isdir(cur):
            return None
        match = next((f for f in os.listdir(cur) if f.lower() == part.lower()), None)
        if match is None:
            return None
        cur = os.path.join(cur, match)
    return cur if os.path.isfile(cur) else None


_BSA_CACHE = {}


def from_bsa(data, rel, bsas):
    """Pull a vanilla asset out of the BSAs into texcache/ (returns None if absent)."""
    for name in bsas:
        if name not in _BSA_CACHE:
            _BSA_CACHE[name] = BSA(os.path.join(data, name))
        bsa = _BSA_CACHE[name]
        if rel.lower() in bsa.files:
            out = os.path.join(CACHE, os.path.basename(rel))
            if not os.path.exists(out):
                open(out, 'wb').write(bsa.read(rel))
            return out
    return None


def find_texture(cfg, game_path):
    key = game_path.lower()
    if key.startswith('data\\'):
        key = key[5:]
    key = key.replace('textures\\', '', 1)
    rel = cfg['remap'].get(key, key.replace('\\', '/'))
    # mod staging dirs (data_roots) take priority over the game Data dir
    for root in cfg.get('data_roots', []) + [cfg['data']]:
        hit = _ci_find(root, 'textures/' + rel)
        if hit:
            return hit
    # opt-in per character: vanilla assets that ship only inside the game's BSAs
    return from_bsa(cfg['data'], 'textures/' + rel, cfg.get('texture_bsas', []))


def to_png(cfg, dds_path, max_size=1024):
    if 'femalehead' in dds_path.lower():
        # bake the facegen tint (skin tone/makeup) over the base head diffuse
        out = os.path.join(CACHE, cfg['head_shape'] + '.png')
        if not os.path.exists(out):
            # SSE facegen: albedo = diffuse * tint * 2 (tint 0.5 = neutral)
            facetint = find_texture(cfg, cfg['facetint'])
            subprocess.run(['magick', dds_path, '-resize', '2048x2048',
                            '(', facetint, '-alpha', 'off', '-resize', '2048x2048', ')',
                            '-compose', 'multiply', '-composite',
                            '-evaluate', 'multiply', '2',
                            '-strip', 'PNG:' + out], check=True)
        return out
    match, factors = cfg['body_bake']
    baked = match in dds_path.lower()  # body/hands skin: match composited face albedo
    # cache key must cover source path + bake, NOT just basename — every character
    # has a femalebody_1.dds, and a basename slot lets one poison the others
    stem = os.path.basename(dds_path).rsplit('.', 1)[0]
    tag = hashlib.md5((dds_path + (repr(factors) if baked else '')).encode()).hexdigest()[:8]
    out = os.path.join(CACHE, f'{stem}.{tag}.png')
    if not os.path.exists(out):
        cmd = ['magick', dds_path, '-resize', f'{max_size}x{max_size}>']
        if baked:
            for ch, f in zip('RGB', factors):
                cmd += ['-channel', ch, '-evaluate', 'multiply', f'{f:.4f}']
            cmd += ['+channel']
        subprocess.run(cmd + ['-strip', 'PNG:' + out], check=True)
    return out


def main(cfg):
    buffers = bytearray()
    accessors, buffer_views, meshes_out, nodes, materials, images, textures_out, samplers = \
        [], [], [], [], [], [], [], [{'magFilter': 9729, 'minFilter': 9987, 'wrapS': 10497, 'wrapT': 10497}]
    png_index = {}

    def add_view(data, target=None):
        while len(buffers) % 4:
            buffers.append(0)
        bv = {'buffer': 0, 'byteOffset': len(buffers), 'byteLength': len(data)}
        if target:
            bv['target'] = target
        buffers.extend(data)
        buffer_views.append(bv)
        return len(buffer_views) - 1

    def add_texture(png_path, srgb):
        if png_path in png_index:
            return png_index[png_path]
        data = open(png_path, 'rb').read()
        # strip the cache-key tag so the embedded name is stable across bake tweaks
        images.append({'bufferView': add_view(data), 'mimeType': 'image/png',
                       'name': re.sub(r'\.[0-9a-f]{8}\.png$', '.png', os.path.basename(png_path))})
        textures_out.append({'sampler': 0, 'source': len(images) - 1})
        png_index[png_path] = len(textures_out) - 1
        return png_index[png_path]

    items = []
    mesh_nodes = []
    seen_geo = set()
    # CBBE 3BA/HDT outfits weight verts to breast/butt bones the vanilla skeleton
    # lacks; those verts lose every influence and collapse. Such a character sets
    # 'skeleton' to XPMSSE (a superset) instead.
    skel = NifFile(os.path.join(cfg['data'], cfg.get(
        'skeleton', 'meshes/actors/character/character assets female/skeleton_female.nif')))
    skeleton = skel.node_globals

    # skeleton -> glTF joint nodes (parents before children)
    joint_index = {}
    order = []
    remaining = dict(skel.node_tree)
    while remaining:
        for name, (par, _) in list(remaining.items()):
            if par is None or par in joint_index:
                joint_index[name] = len(nodes)
                order.append(name)
                t, r9, s = remaining.pop(name)[1]
                nodes.append({'name': name, 'translation': list(t),
                              'rotation': quat_from_mat3(r9), 'scale': [s, s, s]})
    for name in order:
        par = skel.node_tree[name][0]
        if par is not None:
            nodes[joint_index[par]].setdefault('children', []).append(joint_index[name])
    skeleton_roots = [joint_index[n] for n in order if skel.node_tree[n][0] is None]

    for rel, opts in cfg['meshes']:
        nif = NifFile(os.path.join(cfg['data'], rel), skeleton=skeleton)
        for sh in nif.shapes:
            if not sh.positions or not sh.triangles or sh.name in opts.get('skip', ()):
                continue
            geo_key = (len(sh.positions), len(sh.triangles), sh.textures[0] if sh.textures else '')
            if geo_key in seen_geo:  # facegen ships hair twice (opaque+blend pass)
                continue
            seen_geo.add(geo_key)
            pos = sh.positions
            if sh.transform is not None:
                m = sh.transform
                pos = [(m[0][0]*x + m[0][1]*y + m[0][2]*z + m[0][3],
                        m[1][0]*x + m[1][1]*y + m[1][2]*z + m[1][3],
                        m[2][0]*x + m[2][1]*y + m[2][2]*z + m[2][3]) for x, y, z in pos]
            items.append([rel, opts, sh, pos, None])

    # smooth normals across ALL recomputed meshes so seams (neck, wrists, ankles) agree
    soup_pos, soup_tris, spans = [], [], []
    for it in items:
        rel, opts, sh, pos, _ = it
        if sh.normals and any(k in sh.name.lower() for k in ('hair', 'eyes')):
            it[4] = sh.normals
            continue
        base = len(soup_pos)
        soup_pos.extend(pos)
        soup_tris.extend((a + base, b + base, c + base) for a, b, c in sh.triangles)
        spans.append((it, base, len(pos)))
    soup_normals = smooth_normals(soup_pos, soup_tris)
    for it, base, n in spans:
        it[4] = soup_normals[base:base + n]

    for rel, opts, sh, pos, normals in items:
            pdata = struct.pack('<%df' % (3 * len(pos)), *[c for v in pos for c in v])
            mins = [min(v[i] for v in pos) for i in range(3)]
            maxs = [max(v[i] for v in pos) for i in range(3)]
            accessors.append({'bufferView': add_view(pdata, 34962), 'componentType': 5126,
                              'count': len(pos), 'type': 'VEC3', 'min': mins, 'max': maxs})
            attrs = {'POSITION': len(accessors) - 1}

            ndata = struct.pack('<%df' % (3 * len(normals)),
                                *[c for v in normals for c in v])
            accessors.append({'bufferView': add_view(ndata, 34962), 'componentType': 5126,
                              'count': len(normals), 'type': 'VEC3'})
            attrs['NORMAL'] = len(accessors) - 1
            if sh.uvs:
                udata = struct.pack('<%df' % (2 * len(sh.uvs)), *[c for v in sh.uvs for c in v])
                accessors.append({'bufferView': add_view(udata, 34962), 'componentType': 5126,
                                  'count': len(sh.uvs), 'type': 'VEC2'})
                attrs['TEXCOORD_0'] = len(accessors) - 1

            joints, wts = [], []
            for vw in sh.skin_weights:
                top = sorted(((w, b) for b, w in vw if w > 0 and b in joint_index),
                             reverse=True)[:4]
                tw = sum(w for w, _ in top) or 1.0
                top += [(0.0, None)] * (4 - len(top))
                joints.append([joint_index[b] if b else 0 for _, b in top])
                wts.append([w / tw for w, _ in top])
            if joints:
                jdata = struct.pack('<%dH' % (4 * len(joints)), *[j for v in joints for j in v])
                accessors.append({'bufferView': add_view(jdata, 34962), 'componentType': 5123,
                                  'count': len(joints), 'type': 'VEC4'})
                attrs['JOINTS_0'] = len(accessors) - 1
                wdata = struct.pack('<%df' % (4 * len(wts)), *[w for v in wts for w in v])
                accessors.append({'bufferView': add_view(wdata, 34962), 'componentType': 5126,
                                  'count': len(wts), 'type': 'VEC4'})
                attrs['WEIGHTS_0'] = len(accessors) - 1

            idata = struct.pack('<%dH' % (3 * len(sh.triangles)),
                                *[i for t in sh.triangles for i in t])
            accessors.append({'bufferView': add_view(idata, 34963), 'componentType': 5123,
                              'count': 3 * len(sh.triangles), 'type': 'SCALAR'})
            indices = len(accessors) - 1

            mat = {'name': sh.name,
                   'pbrMetallicRoughness': {'metallicFactor': 0.0, 'roughnessFactor': 0.55},
                   'doubleSided': True}
            tint = opts.get('tint')
            if tint is None and 'hair' in sh.name.lower():
                tint = cfg['hair_tint']
            if tint:
                mat['pbrMetallicRoughness']['baseColorFactor'] = [*tint, 1.0]
            diffuse = find_texture(cfg, sh.textures[0]) if sh.textures else None
            if diffuse:
                mat['pbrMetallicRoughness']['baseColorTexture'] = {
                    'index': add_texture(to_png(cfg, diffuse), True)}
            elif 'mouth' in sh.name.lower():
                mat['pbrMetallicRoughness']['baseColorFactor'] = [0.23, 0.12, 0.10, 1.0]
            if sh.alpha_flags is not None:
                if sh.alpha_flags & 1:  # alpha blend
                    mat['alphaMode'] = 'BLEND'
                elif sh.alpha_flags & 0x200:  # alpha test
                    mat['alphaMode'] = 'MASK'
                    mat['alphaCutoff'] = sh.alpha_threshold / 255.0
            materials.append(mat)

            prim = {'attributes': attrs, 'indices': indices, 'material': len(materials) - 1}
            mesh_def = {'name': sh.name, 'primitives': [prim]}
            if sh.name == cfg['head_shape']:
                tri = TriFile(BSA(os.path.join(cfg['data'], cfg['head_tri_bsa'])).read(cfg['head_tri']))
                assert tri.num_verts == len(pos), (tri.num_verts, len(pos))
                m = sh.bone_mats['NPC Head [Head]']  # deltas live in head-local space
                targets, tnames = [], []
                for mname in cfg['morph_names']:
                    deltas = tri.morphs[mname]
                    world = [(m[0][0]*dx + m[0][1]*dy + m[0][2]*dz,
                              m[1][0]*dx + m[1][1]*dy + m[1][2]*dz,
                              m[2][0]*dx + m[2][1]*dy + m[2][2]*dz) for dx, dy, dz in deltas]
                    tdata = struct.pack('<%df' % (3 * len(world)), *[c for v in world for c in v])
                    tmins = [min(v[i] for v in world) for i in range(3)]
                    tmaxs = [max(v[i] for v in world) for i in range(3)]
                    accessors.append({'bufferView': add_view(tdata, 34962), 'componentType': 5126,
                                      'count': len(world), 'type': 'VEC3',
                                      'min': tmins, 'max': tmaxs})
                    targets.append({'POSITION': len(accessors) - 1})
                    tnames.append(mname)
                prim['targets'] = targets
                mesh_def['weights'] = [0.0] * len(targets)
                mesh_def['extras'] = {'targetNames': tnames}
                print(f'  + {len(targets)} morph targets on {sh.name}')
            meshes_out.append(mesh_def)
            node = {'name': sh.name, 'mesh': len(meshes_out) - 1}
            if 'JOINTS_0' in attrs:
                node['skin'] = 0
            mesh_nodes.append(len(nodes))
            nodes.append(node)
            print(f'{rel.split("/")[-1]} :: {sh.name}: {len(pos)}v, tex={diffuse and os.path.basename(diffuse)}')

    # root: Z-up -> Y-up (rotate -90deg about X), scale to meters; owns the skeleton.
    # Skinned meshes sit at scene level — joints alone place them, so the root
    # transform applies exactly once (via the bones).
    s = 0.01428
    root = {'name': cfg['name'], 'children': skeleton_roots,
            'matrix': [-s, 0, 0, 0,  0, 0, s, 0,  0, s, 0, 0,  0, 0, 0, 1]}  # column-major: Z-up -> Y-up, facing +Z
    root_index = len(nodes)
    nodes.append(root)

    ibm = []
    for name in order:
        inv = affine_inverse(skeleton[name])
        ibm.extend(inv[r][c] for c in range(4) for r in range(4))  # column-major
    accessors.append({'bufferView': add_view(struct.pack('<%df' % len(ibm), *ibm)),
                      'componentType': 5126, 'count': len(order), 'type': 'MAT4'})
    skins = [{'joints': [joint_index[n] for n in order],
              'inverseBindMatrices': len(accessors) - 1,
              'skeleton': skeleton_roots[0]}]

    gltf = {
        'asset': {'version': '2.0', 'generator': 'lydia nif2gltf'},
        'scene': 0,
        'scenes': [{'nodes': [root_index] + mesh_nodes}],
        'skins': skins,
        'nodes': nodes,
        'meshes': meshes_out,
        'materials': materials,
        'accessors': accessors,
        'bufferViews': buffer_views,
        'samplers': samplers,
        'images': images,
        'textures': textures_out,
        'buffers': [{'byteLength': len(buffers)}],
    }

    j = json.dumps(gltf, separators=(',', ':')).encode()
    j += b' ' * (-len(j) % 4)
    while len(buffers) % 4:
        buffers.append(0)
    glb = struct.pack('<III', 0x46546C67, 2, 28 + len(j) + len(buffers))
    glb += struct.pack('<II', len(j), 0x4E4F534A) + j
    glb += struct.pack('<II', len(buffers), 0x004E4942) + bytes(buffers)
    open(cfg['out'], 'wb').write(glb)
    print(f'\nwrote {cfg["out"]}: {len(glb)/1e6:.2f} MB, {len(meshes_out)} meshes, {len(images)} textures')



def load(name):
    """A character's config. One module per character — they share code, never state."""
    return importlib.import_module(f'characters.{name}').CONFIG


def sha(path):
    return hashlib.sha256(open(path, 'rb').read()).hexdigest()


def hash_file(name):
    return os.path.join(CHARACTERS, name + '.sha256')


if __name__ == '__main__':
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('names', nargs='*', help='characters to build (default: all)')
    ap.add_argument('--verify', action='store_true',
                    help='build to a scratch file and fail if the output moved')
    ap.add_argument('--freeze', action='store_true',
                    help='record the current output hashes as the baseline for --verify')
    a = ap.parse_args()
    names = a.names or sorted(f[:-3] for f in os.listdir(CHARACTERS) if f.endswith('.py'))

    failed = []
    for name in names:
        cfg = dict(load(name))
        if a.verify:
            cfg['out'] = os.path.join(CACHE, name + '.verify.glb')
        main(cfg)
        if a.freeze:
            open(hash_file(name), 'w').write(sha(cfg['out']) + '\n')
            print(f'froze {name}')
        elif a.verify:
            want = open(hash_file(name)).read().strip()
            got = sha(cfg['out'])
            os.remove(cfg['out'])
            print(f'{name}: {"ok" if got == want else f"CHANGED\n  was {want}\n  now {got}"}')
            if got != want:
                failed.append(name)
    if failed:
        raise SystemExit(f'output changed for: {", ".join(failed)}')
