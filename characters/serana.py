"""Serana — Seranaholic 1.8.4 sculpt in the Twilight Princess Armor Mashup."""
import os

from build_glb import DATA, HEAD_TRI, MORPH_NAMES, ROOT, TEXTURE_BSAS

# Seranaholic 1.8.4 FOMOD, read straight out of the extracted download rather
# than a deployed install, so the option dirs pick themselves: CBBE + Red eye.
SERANAHOLIC = os.path.expanduser(
    '~/Downloads/Seranaholic SSE 1.8-13027-1-8-5-1642185840/'
    'Seranaholic SSE 1.8-FIXED/Seranaholic 1.8.4')
OPTIONS = ['01 Option/Eye color/Red eye', '01 Option/CBBE', '00 Main']

# Twilight Princess Armor Mashup (Vortex staging). The cuirass carries its own
# CBBE body and the gloves carry their own hands, so neither is added here.
TPA = os.path.expanduser(
    '~/.config/steamtinkerlaunch/vortex/staging/skyrimse/mods/'
    'Twilight Princess Armor Mashup-71182-5-2-1719932881')
ARMOR = os.path.join(TPA, 'meshes/Twilight Princess Armor/F')

# the armor is weighted to CBBE 3BA breast/butt bones, which only exist here
XPMSSE = os.path.expanduser(
    '~/.config/steamtinkerlaunch/vortex/staging/skyrimse/mods/'
    'XP32 Maximum Skeleton Special Extended-1988-5-06-1707663131')

CONFIG = {
    'name': 'Serana',
    'data': DATA,
    # highest priority first: bijin-skin (realistic head + 4K body staged under
    # serana/), then eye choice, then body, then Seranaholic base, then outfit
    'data_roots': [os.path.join(ROOT, 'mods/bijin-skin')]
                  + [os.path.join(SERANAHOLIC, d) for d in OPTIONS] + [TPA],
    # her mouth interior ships only inside the game BSAs
    'texture_bsas': TEXTURE_BSAS,
    'out': os.path.join(ROOT, 'serana.glb'),
    'skeleton': os.path.join(
        XPMSSE, 'meshes/actors/character/character assets female/skeleton_female.nif'),
    'meshes': [
        # City cuirass variant: jacket + corset + pants + CBBE body, no pauldron.
        # Cloak and tasset dropped by choice; gauntlets skipped to keep just the
        # fingerless gloves.
        (os.path.join(ARMOR, 'TwilightPrincess_Cuirass_City_1.nif'), {}),
        (os.path.join(ARMOR, 'TwilightPrincess_Gloves_1.nif'), {'skip': ('Gauntlet',)}),
        (os.path.join(ARMOR, 'TwilightPrincess_Boots_1.nif'), {}),
        # her face: facegen keyed to Dawnguard.esm, resculpted by Seranaholic
        (os.path.join(SERANAHOLIC, OPTIONS[0], 'meshes/actors/character/'
                      'FaceGenData/FaceGeom/Dawnguard.esm/00002b6c.nif'), {}),
    ],
    # cooler + lower than Lydia's Bijin-measured lift: her reference look is porcelain,
    # and the red-heavy Bijin factors read rosy on Seranaholic skin
    'body_bake': ('character/serana/female', (1.42, 1.42, 1.50)),
    'hair_tint': (0.008, 0.008, 0.012),  # ink black, hint of blue in the sheen
    'facetint': 'actors/character/FaceGenData/FaceTint/Dawnguard.esm/00002B6C.dds',
    'head_shape': 'SeranaHeadHP',
    'head_tri_bsa': HEAD_TRI[0],
    'head_tri': HEAD_TRI[1],
    'morph_names': MORPH_NAMES,
    # the outfit's body and hands ask for vanilla skin -> Seranaholic's
    'remap': {
        r'actors\character\female\femalebody_1.dds': 'actors/character/Serana/femalebody_1.dds',
        r'actors\character\female\femalehands_1.dds': 'actors/character/Serana/femalehands_1.dds',
    },
}
