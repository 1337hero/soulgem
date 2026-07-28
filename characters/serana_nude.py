"""Serana with no outfit — diagnostic build to isolate the wrist artifact."""
import os

from build_glb import ROOT
from characters.serana import CONFIG as BASE, SERANAHOLIC

BODY = os.path.join(SERANAHOLIC, '01 Option/CBBE/meshes/actors/character/Serana')

CONFIG = {
    **BASE,
    'out': os.path.join(ROOT, 'serana_nude.glb'),
    'meshes': [
        (os.path.join(BODY, 'femalebody_1.nif'), {}),
        (os.path.join(BODY, 'femalehands_1.nif'), {}),
        (os.path.join(BODY, 'femalefeet_1.nif'), {}),
        BASE['meshes'][-1],  # her head
    ],
}
