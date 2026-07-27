"""Emit lydia.html — fully self-contained viewer (bundle + GLB inlined), works from file://."""
import base64
import os

d = os.path.dirname(os.path.abspath(__file__))
html = open(os.path.join(d, 'index.html')).read()
bundle = open(os.path.join(d, 'bundle.js')).read()
glb = base64.b64encode(open(os.path.join(d, 'lydia.glb'), 'rb').read()).decode()
# embed a subset of idle clips (all of them would triple the file size)
anims = {}
for n in ('pfi18', 'pfi19ex'):
    p = os.path.join(d, 'anims', n + '.json')
    if os.path.exists(p):
        anims[n] = open(p).read()
anims_js = '{' + ','.join(f'"{k}":{v}' for k, v in anims.items()) + '}'

bundle = bundle.replace('"body.glb"', 'window.LYDIA_GLB').replace("'body.glb'", 'window.LYDIA_GLB')
assert 'window.LYDIA_GLB' in bundle, 'glb path not found in bundle'
inline = (f'<script>window.LYDIA_GLB="data:application/octet-stream;base64,{glb}";'
          f'window.LYDIA_ANIMS={anims_js}</script>\n'
          f'<script type="module">{bundle}</script>')
html = html.replace('<script type="module" src="bundle.js"></script>', inline)

out = os.path.join(d, 'lydia.html')
open(out, 'w').write(html)
print(f'wrote {out}: {os.path.getsize(out)/1e6:.1f} MB')
