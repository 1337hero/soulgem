# Handoff: Lydia Companion — P0–P4 done, P5 underway

Project: `~/Experiments/soulgem` (**Soulgem** / soulgem.ai — renamed from
lydia-viewer 2026-07-27) — swappable-soul local voice companions; first soul
is Skyrim's Lydia (3x R9700). Git repo since 2026-07-27; game-derived
assets, personal memory, and vendored rhubarb are gitignored (rebuild via
build_glb.py/hkx_anim.py, rhubarb via GitHub release). Never revert
unexplained changes — Mike edits in-tree mid-session.

Read first: `COMPANION-PLAN.md` (phase checklist with ✅ notes — the detailed
record), `README.md` (NIF/GLB pipeline), memory `lydia-viewer-nif-pipeline`.

## Where we are (2026-07-27)

P0–P4 all done and browser-verified. P5 in progress:
- ✅ Barge-in, ✅ LLM→TTS sentence streaming (details in plan §6).
- Warm turn: **~6.5s to first audio** (≈1.5s LLM + ≈4.5s TTS synth). TTS RTF
  is the remaining fat — the faster-qwen3-tts ROCm fork sidequest
  (`~/Experiments/voice/faster-qwen3-tts`, targets in plan §5 + note
  `~/Claude/notes/faster-qwen3-tts-rocm-fork.md`) closes it.
- Next P5 items: clothes/armor (Mike researching sets — her steel default +
  others he's used), HKX one-shot gestures, single-card model bake-off
  (Bonsai-27B / Gemma4-12B / GLM-4.7-Flash / Ornith-1.0-9B).
- Also queued: architecture review incl. language question (Python vs
  Go/Rust/TS for the tooling).

## Stack

`nohup bash server/run.sh > <scratchpad>/companion.log 2>&1 &` starts
whisper :8124 + TTS :8123 + orchestrator :8471 (llama-swap :8082 is systemd).
Wait for "warmed up" (~60–90s). LLM: Gemma4-12B via llama-swap
(`server/companion.js` LLM_MODEL), `enable_thinking:false` required.
TTS: Qwen3-TTS 0.6B bf16 clone, `MIOPEN_FIND_MODE=FAST` (critical — RTF 1.4
vs 5.5 on novel sentences without it). Lip sync: Rhubarb (`server/rhubarb/`)
per sentence wav. Memory: `memory/user.jsonl` + `memory/state.json` (meter).

## Server architecture (refactored 2026-07-27, Opus builders + validator)

- `server/protocol.ts` — SINGLE source of truth: WS message types, REPLY_SCHEMA
  (field order is load-bearing: emotion/mood/gesture → reply →
  meter_delta/memory_note — llama.cpp grammar follows declaration order),
  morph vocab (VISEME_KEYS derived from SHAPE_VISEME), EMOTION_MOOD, and
  outputFormatDoc() which GENERATES the persona's Output-format section
  (persona/lydia.md no longer contains one). Client imports morph keys from
  here too; a load-time drift guard warns on morphs missing from the GLB.
- `server/reply_stream.ts` — pure streaming JSON parser (push/finish → events).
  Salvages truncated (max_tokens) replies.
- `server/stages.js` — transcribe/synthesize/lipSync adapters; all service
  URLs env-overridable (LYDIA_ASR_URL/LYDIA_TTS_URL/LYDIA_RHUBARB); temp files
  cleaned in finally. New TTS/ASR backend = new adapter here, config only.
- `server/store.ts` — durable single-user state: memories (user.jsonl,
  append-only) + meter with tiers; renders the prompt's memory/standing
  section. Testable via `new Store(dir)`.
- `server/companion.js` — orchestrator only: WS plumbing, per-connection
  history (ws.data.history, capped 48), turn loop, LLM fetch.
- Tests: `bun test server/` — 15 (reply_stream + store).

WS protocol: `text`/`audio` → turn; `interrupt` cancels. Server sends
`transcript`, `speak` chunks (sentence + visemes + b64 wav + emotion/mood/
meter — NO `last` flag), then `speak_end`. Types in protocol.ts.

## Gotchas (all earned)

- **main.js edits need `bun build main.js --outfile bundle.js --minify`** —
  index.html loads bundle.js; a stale bundle silently runs old client code.
- pkill/pgrep patterns match your own compound command line — kill and
  relaunch in SEPARATE tool calls, bracket-trick patterns (`run\.[s]h`).
- Killing bun fires run.sh's EXIT trap → whole stack dies. Restart = full
  restart (TTS reload ~15s + warmup).
- Benchmark TTS only on NOVEL sentences (MIOpen shape cache lies ~4x).
- HIP_VISIBLE_DEVICES never CUDA_; TTS device 2; bf16 never fp16.
- Headed playwright stopped launching late session (headless fine) —
  untriaged; tests run `headless: true`.
- llama-swap unload: `POST :8082/api/models/unload` (killing pid isn't enough).
