"""Aster — Jordis' Bijin Warmaidens face in the Eilhart dress.

The example soul's librarian, harvested entirely from installed mods: face is
Bijin Jordis (facegen 000A2C8F, hair ships pre-tinted blonde), dress is the
Eilhart/Philippa full-length gown (own CBBE torso, hem hides the feet), hands
are base CBBE remapped to Bijin skin.
"""
import os

from build_glb import DATA, HEAD_TRI, MORPH_NAMES, ROOT, TEXTURE_BSAS

STAGING = os.path.expanduser('~/.config/steamtinkerlaunch/vortex/staging/skyrimse/mods')
BIJIN = os.path.join(STAGING, 'Bijin Warmaidens SE v3.1.3-1825-3-1-3')
EILHART = os.path.join(STAGING, 'Eilhart Dress - SSE CBBE BodySlide-22270-1-0-1546282298')
CBBE = os.path.join(STAGING, "Caliente's Beautiful Bodies Enhancer CBBE - v2.0.2-198-2-0-2-1698759611")

CONFIG = {
    'name': 'Aster',
    'data': DATA,
    # bijin-skin (Bijin Skin CBBE): realistic no-moles head with freckles baked
    # in for jordis/, 4K body — shadows the stock Bijin skin
    'data_roots': [os.path.join(ROOT, 'mods/bijin-skin'), BIJIN, EILHART, CBBE],
    'texture_bsas': TEXTURE_BSAS,  # mouth interior ships only in the game BSAs
    'out': os.path.join(ROOT, 'aster.glb'),
    'meshes': [
        (os.path.join(EILHART, 'meshes/NS/Eilhart/PE_1.nif'), {}),  # dress + CBBE torso, vanilla bones
        (os.path.join(CBBE, 'meshes/actors/character/character assets/femalehands_1.nif'), {}),
        (os.path.join(BIJIN, 'meshes/actors/character/FaceGenData/FaceGeom/skyrim.esm/000A2C8F.NIF'), {}),
    ],
    'body_bake': ('warmaidens 00', (0.9372 * 1.92, 0.8667 * 1.86, 0.8667 * 1.85)),
    'hair_tint': None,  # longbraids_blonde.dds is pre-tinted
    'facetint': 'actors/character/FaceGenData/FaceTint/skyrim.esm/000A2C8F.dds',
    'head_shape': 'JordisHeadHP',
    'head_tri_bsa': HEAD_TRI[0],
    'head_tri': HEAD_TRI[1],
    'morph_names': MORPH_NAMES,
    # dress torso + hands ask for vanilla skin -> Bijin (matches the composited face)
    'remap': {
        r'actors\character\female\femalebody_1.dds': 'actors/character/Bijin Warmaidens 00/femalebody_1.dds',
        r'actors\character\female\femalehands_1.dds': 'actors/character/Bijin Warmaidens 00/femalehands_1.dds',
    },
}
