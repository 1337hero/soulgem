"""Lydia — Bijin Warmaidens sculpt in Girl's Travel Outfit."""
import os

from build_glb import DATA, HEAD_TRI, MORPH_NAMES, ROOT

# Girl's Travel Outfit (Vortex staging, not deployed into Data) — the outfit
# carries its own CBBE body + hands, so it REPLACES femalebody/hands/feet.
GTO = os.path.expanduser(
    "~/.config/steamtinkerlaunch/vortex/staging/skyrimse/mods/"
    "Girl's Travel Outfit CBBE-125910-1-1-2-1727489948")

CONFIG = {
    'name': 'Lydia',
    'data': DATA,
    # bijin-skin symlinks (Bijin Skin CBBE: realistic head, 4K body) shadow the
    # stock Warmaidens skin in Data — first root wins
    'data_roots': [os.path.join(ROOT, 'mods/bijin-skin'), GTO],
    'out': os.path.join(ROOT, 'lydia.glb'),
    'meshes': [
        (os.path.join(GTO, "Meshes/Girl's Travel Outfit/torso_1.nif"), {}),
        (os.path.join(GTO, "Meshes/Girl's Travel Outfit/gloves_1.nif"), {}),
        (os.path.join(GTO, "Meshes/Girl's Travel Outfit/boots_1.nif"), {}),
        (os.path.join(GTO, "Meshes/Girl's Travel Outfit/choker.nif"), {}),
        # her real face: facegen keyed to the origin master (skyrim.esm) = Bijin sculpt
        ('meshes/actors/character/facegendata/facegeom/skyrim.esm/000A2C8E.NIF', {}),
    ],
    # measured albedo gap vs composited face (flat-light probe): the game body shader
    # lifts skin via subsurface/spec that flat PBR lacks (includes QNAM .937/.867/.867)
    'body_bake': ('warmaidens 00', (0.9372 * 1.92, 0.8667 * 1.86, 0.8667 * 1.85)),
    'hair_tint': (0.035, 0.025, 0.018),  # near-black brown, multiplied over grayscale hair diffuse
    'facetint': 'actors/character/FaceGenData/FaceTint/Skyrim.esm/000A2C8E.dds',
    # expression/phoneme morphs for the head (P1): vanilla tri from the BSA
    'head_shape': 'LydiaHeadHP',
    'head_tri_bsa': HEAD_TRI[0],
    'head_tri': HEAD_TRI[1],
    'morph_names': MORPH_NAMES,
    # game texture path (lowercase) -> actual file under Data/textures
    'remap': {
        r"ks hairdo's\dawn.dds": 'actors/character/Lydia/hair/Dawn.dds',
        r"ks hairdo's\dawn_n.dds": 'actors/character/Lydia/hair/Dawn_n.dds',
        r"ks hairdo's\hairline\long.dds": 'actors/character/Lydia/hair/long.dds',
        r"ks hairdo's\hairline\long_n.dds": 'actors/character/Lydia/hair/long_n.dds',
        r'actors\character\eyes\eyebrown.dds': 'actors/character/Lydia/eyes/HumanEyes15.dds',
        r'actors\character\eyes\eyegreen.dds': 'actors/character/Lydia/eyes/eyegreen.dds',
        r'actors\character\eyes\eyebrown_n.dds': 'actors/character/Lydia/eyes/eyebrown_n.dds',

        r'actors\character\female\femalebody_1.dds': 'actors/character/Bijin Warmaidens 00/femalebody_1.dds',
        r'actors\character\female\femalehands_1.dds': 'actors/character/Bijin Warmaidens 00/femalehands_1.dds',
        r'actors\character\female\astridbody.dds': 'actors/character/Bijin Warmaidens 00/femalebody_1.dds',
    },
}
