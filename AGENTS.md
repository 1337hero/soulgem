# AGENTS.md — working on Soulgem

Operational guide for agents (and future Mike). The architecture/state doc is
`COMPANION-PLAN.md`; this file is the how-to.

## Quickstart

```sh
bun start               # whisper :8124 + TTS :8123 + orchestrator :8471
SOUL=example bun start  # soul select (default lydia)
bun run build           # REQUIRED after editing main.js — index.html loads bundle.js
bun test                # server unit tests (reply parser + store)
```

llama-swap on :8082 is systemd-managed and assumed running. GPU status:
`rocm-smi` (never nvidia-smi). `HIP_VISIBLE_DEVICES`, never CUDA_.

## Hard-won rules

- **Stale bundle.js silently runs old client code.** Any main.js (or imported
  protocol.ts) change → `bun run build`. When "nothing changed", check this
  first.
- **kill and relaunch the stack in SEPARATE tool calls** — pkill/pgrep
  patterns match your own compound command line (use `run\.[s]h`-style
  brackets). Killing bun fires run.sh's EXIT trap: whisper + TTS die too;
  there is no partial restart.
- **Benchmark TTS only on NOVEL sentences** — MIOpen caches per tensor shape;
  repeated text lies ~4x. (Applied to the old torch path; the live Vulkan
  engine has no MIOpen, but the novel-sentence rule still holds.)
- **Rhubarb is slower than it looks** — pocketSphinx is ~2.3s init + 0.79x
  audio, single-core, no daemon mode. Don't try to tune one call; it's already
  pipelined so only the first sentence of a turn is on the critical path.
  `LYDIA_RHUBARB_RECOGNIZER=phonetic` is ~5x faster but ignores the transcript
  (Mike judged pocketSphinx's mouth movement better).
- **REPLY_SCHEMA field order is load-bearing** and only REQUIRED fields keep
  their declared order in llama.cpp's grammar — keep every field required.
- Reasoning knobs are per-model: `enable_thinking:false` (Gemma/Qwen/GLM
  family) vs `reasoning_effort:'low'` (gpt-oss — enable_thinking is ignored and
  reasoning eats max_tokens → empty replies). Set in the soul's config.json.
  Symptom of the wrong knob is an EMPTY `content` with a full
  `reasoning_content` — probe a new model before blaming the schema.
- Not everything is committed: `anims/`, `lydia.glb`, `texcache/`, souls
  other than example/ are gitignored (game-derived or personal). Never
  git-add game assets.

## Swapping outfits (Skyrim/CBBE track)

Recipe proven with Girl's Travel Outfit (see the `LYDIA` dict at the bottom
of `build_glb.py` for the live example):

1. **Locate the mod** — Vortex staging:
   `~/.config/steamtinkerlaunch/vortex/staging/skyrimse/mods/<Mod Name>/`.
   Mods do NOT need deploying into game Data — `data_roots` in the config
   resolves meshes/textures straight from staging.
2. **Inspect the NIFs**: `python3 nif.py "<mod>/Meshes/.../torso_1.nif"`
   prints shapes, vert counts, shader types, texture paths. Use weight `_1`
   variants (matches Lydia's Bijin body weight). Skip `_0`, `1stperson*`,
   and `GND/` (ground/inventory models).
3. **Check for included body parts**: CBBE outfits usually ship their own
   body/hands inside the outfit NIFs — look for shapes with `shaderType=5`
   (skin) named like "CBBE"/"Hands". If present, the outfit REPLACES
   femalebody/femalehands/femalefeet in the meshes list (do not double-up;
   boots cover feet).
4. **Textures**: outfit-local paths (`textures\<mod>\...`) resolve via
   `data_roots`. Skin shapes referencing standard paths
   (`actors\character\female\femalebody_1.dds` etc.) are auto-remapped to
   Bijin skin by the existing `remap` entries — this is what keeps body skin
   matching her face. The `body_bake` tint applies to any texture whose path
   contains `warmaidens 00`.
5. **Edit the character config** (`LYDIA` in build_glb.py): swap mesh entries
   (absolute paths into staging are fine), add the mod dir to `data_roots`.
6. **Build + verify**: `python3 build_glb.py` (needs the Skyrim install),
   then view — `bun start` and screenshot front/back/face, or serve
   statically. Check: neck/wrist seams, skin tone match, no floating old
   body parts. `lydia.glb` output is ~15-20MB with an outfit.

New character/body entirely: `main(cfg)` takes a config dict — copy `LYDIA`,
change facegen formid + skin remaps + meshes. Non-Skyrim bodies (VRM etc.)
are a planned separate track (plan §8, SHAPE_VISEME must move to soul
config first).

## Adding animations

Any Skyrim HKX works (vanilla BSA included — empty track names fall back to
`anims/skeleton_track_order.json`):

```sh
python3 -c "from bsa import BSA; b=BSA('<Data>/Skyrim - Animations.bsa'); \
  open('/tmp/x.hkx','wb').write(b.read('meshes/actors/character/animations/<name>.hkx'))"
python3 hkx_anim.py /tmp/x.hkx      # decodes to anims/<name>.json + reindexes
```

Naming controls behavior (client-side, by prefix):
- `gesture_*` — one-shot emotes, played on the model's `gesture` field
- `talk_*` — talking body language, random one per speech chunk
  (`talk_angry*` pool used on annoyed emotion)
- anything else — joins the random idle rotation (loopable clips only;
  female variants live under `animations/female/` in the BSA)

Rename by naming the extracted .hkx before decoding. `--reindex` rebuilds
the index alone. Keep clips ≥3s for idles; shorter one-shots are fine.

## Souls

`souls/<name>/{persona.md, config.json, memory/}` — copy `souls/example/`.
config.json: `model` (llama-swap name), `chat_template_kwargs`, `voice_ref`
(TTS clone wav; omit = server default), `glb` (body, served as /body.glb),
`meter` (bool). Only example/ is committed; souls are personal.

## Scenes (render layer done, plumbing pending)

`viewer.setScene('/path.jpg', {height: 1.6, radius: 10})` — equirect pano
via GroundedSkybox (floor is real geometry; she stands on it). 4K max
(bigger chokes headless swiftshader). CC0 source: Poly Haven tonemapped
JPGs. `viewer.setScene(null)` → void. Stage-1 plumbing (config scenes list,
`scene` schema field, crossfade, persistence) is speced in plan §7c.

## Verification patterns

Playwright headless (headed is broken on this box, untriaged). No playwright
package is installed globally — `bun add playwright-core` in a scratch dir and
point `executablePath` at
`~/.cache/ms-playwright/chromium-1228/chrome-linux64/chrome` (note
`chrome-linux64`, not `chrome-linux`). Launch with `--use-gl=swiftshader
--enable-unsafe-swiftshader`.

WS probe without a browser: connect ws://localhost:8471/ws, send
`{type:'text', text}`, expect `state` (on connect) → `transcript` →
`speak`×N → `speak_end`. `window.viewer` in the page: `say()`, `setMorph`,
`playIdle`, `playGesture`, `setScene`, `gaze` (live-tunable), `THREE` exposed
for experiments.

UI paths worth re-checking after client edits: `I` opens the text input and
`Esc` closes it; **typing must not trigger push-to-talk** (window keydown
handlers bail on INPUT/TEXTAREA — 't' is in most words, so a regression here
opens the mic mid-sentence).
