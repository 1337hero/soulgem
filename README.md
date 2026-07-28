# Soulgem

**soulgem.ai** — put a soul in anything. Fully-local voice-driven companions
built from game characters: push-to-talk → whisper → LLM persona →
cloned-voice TTS → lip-synced, animated reply in the browser. Persona, voice,
model, and body are all swappable — the first soul is Lydia (Bijin
Warmaidens), rendered from Skyrim SE game files via three.js. Runs entirely
on this machine (3x R9700).

## Run the companion

Prerequisites (all machine-local, paths hardcoded in `server/run.sh`):
- llama-swap already serving on :8082 (systemd) with the soul's model
  registered (Lydia: `GLM-4.7-Flash`) — the stack assumes it, run.sh does not
  start it
- whisper.cpp Vulkan build + large-v3-turbo model (paths in run.sh)
- Qwen3-TTS engine at `~/Experiments/voice/qwen3-tts-fast` (qwentts.cpp/Vulkan,
  Q6_K — no torch, no ROCm; `server/tts_server.py` is the old torch fallback)

```sh
bun start               # whisper :8124 + TTS :8123 + orchestrator :8471
SOUL=example bun start  # pick a different soul (default: lydia)
```

## Souls

A soul is a directory under `souls/<name>/`: `persona.md` (character sheet),
`config.json` (LLM model + reasoning kwargs, `voice_ref` wav for the TTS
clone, `glb` body, `meter` on/off, optional `tiers` (per-soul relationship-tier
prose), optional `lighting` — per-soul override
of the viewer's light rig: exposure + ambient/key/fill/rim color/intensity;
omit it and the stock rig applies), and `memory/` (that soul's durable
memories + relationship meter). Copy `souls/example/` (Aster, a lighthouse
librarian) to start your own. Everything under `souls/` except the example is
gitignored — souls are personal.

Wait for `warmed up` in the output (TTS loads in ~1s; the stack also warms the
soul's LLM at launch so the first turn doesn't pay llama-swap's cold load),
then open **http://localhost:8471**.

- **Voice** — hold the talk button or `T`, release to send.
- **Text** — `I` (or the ⌨ button) swaps the talk button for an input; Enter
  sends, the field stays open, `Esc` returns to voice. The choice persists
  across reloads. `viewer.say('Hello Lydia')` still works from the console.
- Talking or typing while she speaks barges in and cancels her turn.

Ctrl-C stops everything (run.sh's EXIT trap kills whisper + TTS with it —
there is no partial restart; killing the orchestrator restarts the stack).

Service URLs are env-overridable: `LYDIA_ASR_URL`, `LYDIA_TTS_URL`,
`LYDIA_RHUBARB`, `LYDIA_RHUBARB_RECOGNIZER` (see `server/stages.js`),
`TTS_PORT`/`TTS_REF` (the TTS project's `server.py`).

## Layout

| Piece | Role |
|---|---|
| `server/run.sh` | starts the stack (`bun start`) |
| `server/companion.js` | orchestrator: WS plumbing, per-connection history, turn loop, LLM call |
| `server/store.ts` | durable state: memories (append-only user.jsonl) + relationship meter/tiers |
| `server/protocol.ts` | single source of truth: WS message types, LLM reply schema (field order is load-bearing), morph vocabulary, generated persona output-format |
| `server/reply_stream.ts` | pure streaming-JSON reply parser (`bun test server/`) |
| `server/stages.js` | transcribe / synthesize / lipSync adapters (whisper, Qwen3-TTS, Rhubarb) |
| `server/tts_server.py` | old torch/ROCm TTS — fallback only; run.sh launches `~/Experiments/voice/qwen3-tts-fast/server.py` (Vulkan, ~19x faster) |
| `souls/<name>/` | soul pack: persona + config + voice ref + per-soul memory |
| `AGENTS.md` | how-to: swap outfits/bodies, add animations, souls, scenes |
| `main.js` | three.js client: viewer, idle anims, viseme lipsync, WS voice loop |
| `COMPANION-PLAN.md` | phase plan + status |

**After editing `main.js` (or anything it imports): `bun run build`** —
index.html loads `bundle.js`, not `main.js`; a stale bundle silently runs old
client code.

## Asset pipeline (offline, already baked)

1. `nif.py` — minimal Skyrim NIF parser (LE NiTriShape + SSE BSTriShape/
   BSDynamicTriShape). Bind-pose skinning via NiSkinData, bone globals from
   `skeleton_female.nif` by name (facegen NIFs carry identity bone stubs).
2. `bsa.py` / `tri.py` — BSA v105 extractor (lz4) and FaceGen TRI morph
   parser (16 visemes, blinks, brows, 7 moods → glTF morph targets).
3. `hkx_anim.py` — decodes SSE Havok spline-compressed animations to
   `anims/*.json` (format documented in its docstring) and regenerates
   `anims/index.json`; `--reindex` rebuilds the index alone. Any Skyrim
   animation is importable.
4. `build_glb.py` — assembles a character: mod/facegen NIFs + real facegen
   head (resolved by ORIGIN master), face tint baked diffuse×tint×2, DDS→PNG
   via ImageMagick (`texcache/`, keyed by source path + bake so characters
   can't poison each other), smooth normals across seams (hair/eyes keep NIF
   normals). One config module per character in `characters/<name>.py`.
   Shared stdlib math in `mathutil.py` (`python3 mathutil.py` self-checks).
5. `voice_ref.py` — builds a TTS clone reference from any Skyrim voice type
   (fuz → xwma → 60s of 24 kHz mono), e.g.
   `python3 voice_ref.py dlc1seranavoice souls/serana/voice_ref.wav`.

```sh
python3 build_glb.py lydia      # rebuild one character (needs the Skyrim install)
python3 build_glb.py --verify   # rebuild ALL to scratch, fail if any output moved
python3 build_glb.py <name> --freeze   # bless a new look as the baseline
bun run build                   # rebuild bundle.js
```

`make_standalone.py` emits `lydia.html` (static viewer only, GLB inlined,
opens from file://) — predates the voice loop and doesn't include it.

## While she talks

Rhubarb visemes drive the mouth per sentence; vanilla dialogue animations
(`talk_*` clips) give her body language while speaking; one-shot gestures
(wave/salute/laugh/applaud/point) fire when the model picks them; 10 idle
stances rotate between turns; she keeps eye contact with the camera
(`viewer.gaze` tunables). Scenes: `viewer.setScene('/pano.jpg', {height,
radius})` places her in an equirect panorama with a real floor (CC0 panos
from Poly Haven work great at 4K). See AGENTS.md for adding any of these.
