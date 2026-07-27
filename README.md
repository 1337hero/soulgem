# Soulgem

**soulgem.ai** — put a soul in anything. Fully-local voice-driven companions
built from game characters: push-to-talk → whisper → LLM persona →
cloned-voice TTS → lip-synced, animated reply in the browser. Persona, voice,
model, and body are all swappable — the first soul is Lydia (Bijin
Warmaidens), rendered from Skyrim SE game files via three.js. Runs entirely
on this machine (3x R9700).

## Run the companion

Prerequisites (all machine-local, paths hardcoded in `server/run.sh` /
`server/tts_server.py`):
- llama-swap already serving on :8082 (systemd) with the `Gemma4-12B` model
  registered — the stack assumes it, run.sh does not start it
- whisper.cpp Vulkan build + large-v3-turbo model (paths in run.sh)
- Qwen3-TTS venv at `~/Experiments/voice/qwen-tts-env` (torch ROCm, bf16)

```sh
bun start               # whisper :8124 + TTS :8123 + orchestrator :8471
SOUL=example bun start  # pick a different soul (default: lydia)
```

## Souls

A soul is a directory under `souls/<name>/`: `persona.md` (character sheet),
`config.json` (LLM model + reasoning kwargs, `voice_ref` wav for the TTS
clone, `glb` body, `meter` on/off), and `memory/` (that soul's durable
memories + relationship meter). Copy `souls/example/` (Aster, a lighthouse
librarian) to start your own. Everything under `souls/` except the example is
gitignored — souls are personal.

Wait for `warmed up` in the output (~60–90s: TTS model load + warmup gen),
then open **http://localhost:8471**. Hold the talk button or the `T` key to
speak; release to send. Text path without a mic: open the console and
`viewer.say('Hello Lydia')`. Talking while she speaks barges in and cancels
her turn.

Ctrl-C stops everything (run.sh's EXIT trap kills whisper + TTS with it —
there is no partial restart; killing the orchestrator restarts the stack).

Service URLs are env-overridable: `LYDIA_ASR_URL`, `LYDIA_TTS_URL`,
`LYDIA_RHUBARB` (see `server/stages.js`), `TTS_PORT`/`TTS_MODEL`
(tts_server.py). First LLM turn after idle includes llama-swap loading the
model (~10s).

## Layout

| Piece | Role |
|---|---|
| `server/run.sh` | starts the stack (`bun start`) |
| `server/companion.js` | orchestrator: WS plumbing, per-connection history, turn loop, LLM call |
| `server/store.ts` | durable state: memories (append-only user.jsonl) + relationship meter/tiers |
| `server/protocol.ts` | single source of truth: WS message types, LLM reply schema (field order is load-bearing), morph vocabulary, generated persona output-format |
| `server/reply_stream.ts` | pure streaming-JSON reply parser (`bun test server/`) |
| `server/stages.js` | transcribe / synthesize / lipSync adapters (whisper, Qwen3-TTS, Rhubarb) |
| `server/tts_server.py` | Qwen3-TTS 0.6B bf16 + cloned Lydia voice, ROCm (`MIOPEN_FIND_MODE=FAST` is load-bearing) |
| `souls/<name>/` | soul pack: persona + config + voice ref + per-soul memory |
| `main.js` | three.js client: viewer, idle anims, viseme lipsync, WS voice loop |
| `COMPANION-PLAN.md` | phase plan + status; `HANDOFF.md` — current working state |

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
4. `build_glb.py` — `main(cfg)` assembles a character from a config dict
   (see `LYDIA`): Bijin body + real facegen head (`facegeom/skyrim.esm/
   000A2C8E.NIF` — facegen resolves by ORIGIN master), face tint baked
   diffuse×tint×2, DDS→PNG via ImageMagick (`texcache/`), smooth normals
   across seams (hair/eyes keep NIF normals). Shared stdlib math in
   `mathutil.py` (`python3 mathutil.py` self-checks).

```sh
python3 build_glb.py   # rebuild lydia.glb (needs the Skyrim install)
bun run build          # rebuild bundle.js
```

`make_standalone.py` emits `lydia.html` (static viewer only, GLB inlined,
opens from file://) — predates the voice loop and doesn't include it.
