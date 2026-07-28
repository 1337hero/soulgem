# Soulgem (né Lydia Companion) — Plan

Goal: turn the static Lydia viewer into an interactive, voice-driven animated
companion — the Ani architecture, but 100% local on ArchBox (3x R9700, 96GB).

## 1. What the Ani teardown tells us

From the transcript, the gist (Grok companion system prompts), and the
architecture diagram:

**Pipeline** (per conversation turn):
```
mic → ASR → [context manager: persona prompt + memory + relationship meter]
    → LLM → reply_text + emotion tags + avatar_actions + bg_prompt (JSON-ish)
    → TTS (emotion-conditioned) → audio + synced avatar animation
```

**The LLM contract is the whole product.** Ani is "just" a system prompt that
makes the model emit, alongside the reply text:
- emotion tags inline (drives TTS delivery + face)
- avatar action calls: `{curiosity, shyness, excitement, love, stress,
  sadness, frustration}` × `{sway, peek, spin, tease}`
- environment detection: `{"environment_change": bool, "environment_prompt",
  "ambient_sound_prompt"}`
- relationship meter deltas (+3..+10 compliments, -8..-14 rude), 0–100 across
  5 states (zero/neutral/interested/attracted/intimate); meter tier selects
  voice style and unlocks behavior

**Failure modes to avoid** (all observed in the video):
1. *State wipes* — users lost intimacy level 7 to an app update. Memory must
   be durable, local, versioned. Ours is a file/DB we own.
2. *Transcription loss* — voice → text → voice discards prosody/emotion.
   Can't fully fix locally yet, but ASR that keeps some paralinguistics
   (whisper gives us nothing here — accept for v1) + emotion-capable TTS
   narrows the gap.
3. *Tag leakage* — "she said giggles out loud." Post-processor must strip
   stage directions from the TTS string; tags live in JSON fields, never in
   the spoken text.
4. *Latency* — every module serial = noticeable delay. Stream everything:
   sentence-chunked TTS starts speaking while the LLM is still generating.
5. *Blandness* — a system prompt alone is thin. We can go further locally:
   persona file + evolving per-user knowledge base injected into context
   (the video's "diverging knowledge bases" idea) — trivially done with files.

## 2. Module map: Ani → our stack

| Ani module          | Local equivalent                                             | Status |
|---------------------|--------------------------------------------------------------|--------|
| ASR                 | whisper.cpp (Vulkan, proven) default; evaluate Voxtral-Mini (vLLM) / Qwen3-Omni later — adapter speaks OpenAI `/v1/audio/transcriptions` so swap is config | **have** |
| LLM                 | llama-swap :8082 — GLM-4.7-Flash (per-soul, any resident model) | **have** |
| Context manager     | Bun orchestrator (WebSocket server), persona.md + memory store | build |
| TTS                 | **Qwen3-TTS + cloned Lydia voice**, Vulkan/qwentts.cpp Q6_K — `~/Experiments/voice/qwen3-tts-fast` (RTF 0.233, 4.29x realtime, TTFA 280ms; `server.py` on :8123). Kokoro-82M fallback no longer needed | **DONE** |
| Avatar actions      | three.js: morph targets (TRI phonemes/expressions) + procedural + clips | build |
| Vision analyzer     | optional later: webcam → Qwen-VL / SmolVLM on llama-swap     | defer |
| Background gen      | optional later: local SDXL/Flux on a spare R9700             | defer |
| Music service       | optional later                                               | defer |
| User profile DB     | SQLite (or plain JSONL) — ours, never wiped                  | build |
| Asset CDN           | local http server (already have)                             | have |

## 3. The asset gap (what Grok can't do and we can)

Current GLB is pre-baked bind-pose soup — fine for a statue, useless for
animation. But `nif.py` already parses everything needed and we own the
pipeline:

**3a. Skinned export.** Export real glTF skinning: skeleton hierarchy from
`skeleton_female.nif`, JOINTS_0/WEIGHTS_0 per vertex (we already read
NiSkinData weights + partition weights — currently we *bake* them; instead
emit them). Result: Lydia poseable in three.js at runtime.

**3b. Face morphs — the killer feature.** Skyrim ships FaceGen `.tri` files
with per-vertex morph deltas for the exact 996-vert head:
- **phonemes** (visemes): Aah, BigAah, BMP, ChJSh, DST, Eee, Eh, FV, I, K,
  N, Oh, OohQ, R, Th, W — game-quality lip sync data
- **expressions**: mood morphs (happy, sad, angry, fear, ...) + BlinkL/R,
  brow movement
Parse TRI (documented format), emit as glTF morph targets on the head mesh.
`SM_Astrid/Head/FemaleHead.tri` is on disk; vanilla one extractable from BSA
(v105, lz4) if vertex order mismatches. Deltas apply cleanly to our
facegen-morphed head as long as vertex count/order match (they derive from
the same base head).

**3c. Body animation, staged:**
1. *Procedural idle* (cheap, ~90% of perceived life): breathing (chest
   scale/spine bone), weight sway, head+eyes look-at cursor/camera, blink
   timer, micro head motions while speaking. Pure three.js bone math.
2. *Gesture clips*: retarget Mixamo clips to the Skyrim skeleton
   (SkeletonUtils.retarget or a Blender pass) — wave, hair tuck, lean, spin.
   Map to the avatar_actions vocabulary.
3. ~~(Stretch)~~ ✅ **DONE 2026-07-26** — `hkx_anim.py` decodes SSE
   hkaSplineCompressedAnimation directly (hkxc → XML → spline/40-bit-quat
   decode ported from HavokLib). PrettyFemaleIdles' 5 idle loops decoded to
   `anims/*.json`; viewer plays them with crossfade + random cycling,
   look-at layered on top. ANY Skyrim animation is now importable —
   P3 gesture clips come from this pipeline, not Mixamo retargeting.

**3d. Clothes.** Extract her steel armor set (or any outfit) from BSAs —
same NIF pipeline, worn meshes are skinned to the same skeleton. Needs the
BSA extractor (~80 lines + lz4). Also fixes the current nudity.

## 4. LLM output contract (v1)

System prompt produces strict JSON per turn (llama.cpp grammar/json_schema
enforced — no leakage by construction):

```json
{
  "reply": "spoken text only, no stage directions",
  "emotion": "warm|teasing|excited|soft|sad|neutral",
  "gesture": "none|sway|tilt|lean_in|hair_tuck|wave|spin",
  "expression": {"happy": 0.6, "surprise": 0.1},
  "meter_delta": 3,
  "memory_note": "optional fact worth persisting about the user"
}
```

Orchestrator: applies meter, appends memory_note to the knowledge base,
strips/validates, fans out — reply→TTS, emotion→TTS style + face, gesture→
animation queue. Persona lives in `persona/lydia.md` (housecarl backstory,
sworn-to-carry-your-burdens deadpan, loyalty arc); memory in
`memory/user.jsonl` — injected into context each turn. Meter tiers gate tone
exactly like the gist (neutral→warm→devoted housecarl).

## 5. Latency budget (target: < 1.5s to first audio)

```
VAD/push-to-talk → whisper small/medium (Vulkan)     ~200-400ms
LLM first sentence (GLM-Air streaming)               ~300-600ms
Qwen3-TTS first sentence (ROCm)                      MEASURE — the P2 gate
```
**TTS engine sidequest — CLOSED 2026-07-27.** Original plan was to port the
CUDA-graph engine to ROCm/HIP (hipGraph primary, Vulkan "experimental"). That
assumption was **backwards**: hipGraph works fine but is irrelevant, because the
PyTorch bottleneck is codec decode, not the talker. Vulkan/qwentts.cpp won by
19x. Engine now lives at `~/Experiments/voice/qwen3-tts-fast` (see its README);
the PyTorch fork is archived in that project's `docs/pytorch-fork-archive/`.

Measured default on this box (0.6B bf16, transformers path, MIOPEN_FIND_MODE=FAST):
RTF 1.4 (0.71x realtime), ~5s per sentence, no streaming. Upstream 4090 baseline
was 0.82x realtime → same class; CUDA graphs took it to 4.78x realtime / 156ms TTFA.

Targets (median over 10 NOVEL sentences — never repeats, the MIOpen shape cache
inflates repeat benchmarks — warm server, same clone prompt, bf16):
- **Gate: ≥2x realtime (RTF ≤ 0.5) + TTFA ≤ 500ms streaming.** The qualitative
  flip: synthesis outruns playback → gapless chained sentences; only the first
  chunk's latency is ever perceived. Warm turn drops to ~llm + 0.5s ≈ 3s.
- **Stretch: ≥3x realtime (RTF ≤ 0.33)** (~4x over default; upstream got 5.8x
  on CUDA, ROCm overhead will eat some).

**Sidequest RESOLVED 2026-07-27 — P2 gate PASSES on Vulkan.** Verified numbers,
10 novel sentences, warm, same clone ref, one R9700:

| | RTF | TTFA | gate |
| --- | --- | --- | --- |
| Vulkan F32 GGUF, `synthesize` | **0.358** (2.79x) | — | pass |
| Vulkan F32 GGUF, `stream` @ `codec_chunk_sec=0.5` | 0.383 | **338ms** (379 worst) | pass |
| Over HTTP through the drop-in server | 0.368–0.402 | **334ms** (357 worst) | pass |
| PyTorch graphs+eager, same sentence | 6.86 | — | fail ~19x |

Stretch (RTF ≤0.33) NOT met in any config. Corrections to the earlier note:
the 0.315 figure came from differencing a load-only run and flattered it ~12%
— **0.358 is the real number**. And the sdpa-codec split idea is **REJECTED by
measurement**, not merely insufficient: sdpa codec decode 29.32s vs eager
30.43s, a 3.6% difference. MIOpen kernel search (`MIOPEN_FIND_ENFORCE=SEARCH`)
also did nothing (30.10s). Neither is a kernel problem.

The real PyTorch bottleneck is CODEC DECODE (91% of wall, 30.4s of 33.5s;
talker alone RTF 0.63) and it is **superlinear**: 8 frames → 0.018 s/frame but
61 frames → 0.49 s/frame — 7.6x the frames, 214x the time, ~27x worse than
linear. `speech_tokenizer.decode` gets the whole sequence in one call.
qwentts.cpp instead exposes `codec_chunk_sec` + `codec_left_context_sec` and
decodes in bounded chunks. That difference, not GPU capability, is the 19x.
Phase-4's ROCm conclusions came from 8-token workloads too short for the codec
to dominate — same benchmark trap as our MIOpen same-sentence RTF lie.

§5's "hipGraph primary / Vulkan experimental" assumption is INVERTED: Vulkan is
the fast path on AMD. Listening review done (Mike, 10 clips + A/B vs PyTorch —
"all sound normal"); samples kept at
`~/Experiments/voice/qwen3-tts-fast/samples/`.

**Wiring: the TTS engine now lives in its own consolidated project**,
`~/Experiments/voice/qwen3-tts-fast/` (see its README). Its `server.py` is a
drop-in for `tts_server.py` — same `POST /speak {"text"} -> audio/wav` and
`GET /health` on the same port 8123, so no client changes. Adds
`POST /speak_stream` (chunked) for the low-TTFA path. Needs no torch, no ROCm:

```bash
cd ~/Experiments/voice/qwen3-tts-fast && runtime/bin/python server.py
```

Loads in **0.8s** on the Q8_0 talker. Env: `TTS_PORT` (8123), `TTS_REF`,
`TTS_GGUF_DIR`, `TTS_TALKER`, `TTS_CHUNK_SEC` (0.5),
`GGML_VK_VISIBLE_DEVICES` (1 = PCI 0000:06:00.0 — Vulkan and HIP ordinals
differ, check `rocm-smi` before changing). Deliberately NOT duplicated into
this repo: one copy, in the TTS project. **Wired 2026-07-27**: run.sh launches
it (old torch tts_server.py kept as fallback); Lydia's voice_ref moved to the
TTS project's `voices/`. Verified in-stack: RTF 0.245–0.26 over HTTP.

**Quantisation hits the STRETCH target** (added 2026-07-27). Default is now
**Q6_K: RTF 0.233 (4.29x realtime), TTFA 280ms**, 0.72GB vs F32's 3.41GB, load
0.63s vs 2.27s — smaller AND faster, less memory bandwidth per token. Mike A/B'd
Q6_K vs Q8_0 and heard no difference (3 sentences each), so the faster one wins;
Q8_0 stays as fallback since Q6_K's RMS does measure lower (0.0629 vs 0.0734).
Over HTTP with Q6_K: median RTF 0.274 (3.65x realtime).

Turn budget with TTS at 334ms TTFA: whisper 200-400 + LLM 300-600 + TTS 334 =
**0.83–1.33s to first audio**, inside the 1.5s target. Warm turn ~6.5s → ~3s,
sentence chaining gapless.

**Post-wiring reality check (2026-07-27): Rhubarb was the unbudgeted stage.**
§5's "CPU, fraction-of-realtime" claim is WRONG for the default recognizer:
pocketSphinx ≈ 2.3s lazy init + 0.79×audio per invocation, pinned to ONE core
(`--threads` only splits across VAD utterances — useless on single sentences).
phonetic is ~5x faster (RTF 0.13–0.23) but ignores the transcript; Mike judged
pocketSphinx's mouth movement better. The fix wasn't speed but scheduling: 8
concurrent rhubarbs cost only 15% more wall than 1, so companion.js now runs
per-sentence synth+lipsync concurrently (sends stay seq-ordered via a promise
chain; barge-in bails before synth/lipsync/send). Result: chaining is gapless
(4 chunks / 13.9s audio all delivered by 8.4s wall) and text-path warm first
audio is ~6.1–6.5s, all of it seq-0's irreducible LLM + TTS + pocketSphinx on
the first sentence. Levers if that ever matters: `LYDIA_RHUBARB_RECOGNIZER=
phonetic` (~3s faster) or phonetic-for-seq-0 hybrid.

Caveats: F32 GGUF (Q8_0 untested, likely faster); requests are serialised
behind a lock since `qt_synthesize` is not reentrant per context; the voice ref
is re-encoded per call because cached refs need qwentts.cpp ABI v2, which the
wrapper-compatible build does not export (see the fork-skew note below);
`do_sample=False` runs away to the token cap on this path — never use greedy.

Stream by sentence: TTS + viseme schedule per sentence while LLM continues.
Viseme timing: Qwen3-TTS gives no phoneme durations → **Rhubarb lip-sync on
each sentence wav is the primary path** (CPU, fraction-of-realtime, outputs
phoneme track → map to TRI visemes). If Qwen RTF is too slow for chat, keep
the cloned voice and fall back to Kokoro only if unacceptable — measure
before deciding. All three services sit behind OpenAI-compatible endpoints
so any swap is a config line, not a refactor.

## 6. Phases (each independently verifiable)

- **P0 — skinned GLB**: export joints/weights/skeleton; verify in three.js
  by rotating a bone at runtime. *Done when: head turns without re-export.*
  ✅ **DONE 2026-07-26** — 154 joints, 9 skinned meshes, IBMs from
  skeleton_female.nif. Note: GLTFLoader sanitizes bone names
  (`NPC Head [Head]` → `NPC_Head_Head`) — use sanitized names in runtime code.
- **P1 — she's alive**: TRI morphs exported; procedural blink/breath/sway/
  look-at. *Done when: idle Lydia tracks the cursor and blinks.*
  ✅ **DONE 2026-07-26** — vanilla femalehead.tri (996v, exact match) pulled
  via new `bsa.py` (SSE v105 + lz4) and `tri.py` (FRTRI003); 34 morph targets
  on LydiaHeadHP (16 visemes, blinks, brows, squints, 7 moods, LookDown),
  deltas transformed by the head-bone bind matrix. Viewer: blink cycle,
  breathing, cursor look-at w/ micro-motion. `window.viewer.setMorph(name, v)`
  is the runtime face API. Mood morphs overshoot at 1.0 — drive at ≤0.7.
- **P2 — voice loop MVP**: push-to-talk → whisper → GLM persona (JSON
  contract) → **Qwen3-TTS cloned Lydia voice** → audio + naive jaw-flap.
  Bun WS server orchestrates; first task is benchmarking Qwen3-TTS RTF on
  the R9700 (sentence-level). *Done when: spoken question → spoken answer
  in HER voice < 3s.*
- **P3 — real lip sync + emotion**: phoneme-timed visemes, expression
  morphs from emotion field, gesture queue. *Done when: mouth shapes match
  words; smile when teasing.*
  ✅ **DONE 2026-07-27** — Rhubarb 1.14 (`server/rhubarb/`) per sentence wav,
  Preston-Blair→TRI viseme map, replies stream sentence-chunked. Client plays
  via AudioBufferSourceNode on the AudioContext clock (sample-accurate),
  60ms lead, fast-attack/slow-release morph easing in the render loop.
  Emotion→mood via server-side EMOTION_MOOD presets (model's own `mood` is
  usually empty). TTS RTF fixed 5.5→1.4 on novel text (`MIOPEN_FIND_MODE=FAST`
  in tts_server.py — default find mode re-searched conv solutions per novel
  sentence length). Gesture queue still just `idle_switch` — richer gestures
  moved to P5. REMEMBER: edits to main.js need `bun build main.js --outfile
  bundle.js --minify` — index.html loads the bundle.
- **P4 — memory + meter**: SQLite/JSONL profile, relationship meter with
  tone tiers, durable across restarts. *Done when: she remembers yesterday.*
  ✅ **DONE 2026-07-27** — `memory/user.jsonl` (memory_note per turn) +
  `memory/state.json` (meter 0–100, `meter_delta` in schema, 4 tone tiers
  injected into the system prompt). Verified: taught her a fact, restarted
  the server, she used it unprompted. (LLM was Gemma4-12B at the time; now
  GLM-4.7-Flash — see the P5 bake-off below.)
- **P5 — polish/optional**: clothes (BSA extractor + armor), Orpheus TTS
  (emotive tags, laughs), Mixamo gesture clips, webcam vision, background
  gen, barge-in (interrupt her mid-sentence).
  Progress 2026-07-27:
  - ✅ **Barge-in** — talk (or say()) over her cancels the turn: client stops
    source + flushes queue + sends `interrupt`; server cancels the in-flight
    sentence loop via per-socket token (also fires on any new turn).
  - ✅ **LLM→TTS sentence streaming** — `thinkStream()` async generator:
    llama.cpp `stream:true`, schema order emotion/mood/gesture → reply →
    meter_delta/memory_note (llama.cpp grammar follows declaration order, so
    face metadata arrives before the reply text and bookkeeping after), reply
    string unescaped incrementally, sentences dispatched to TTS mid-generation.
    `speak` chunks have no `last` flag anymore; `speak_end` closes the turn.
    Warm-turn first audio 8–11s → **~6.5s** regardless of reply length.
    Chunk gaps remain audible (RTF 1.4 < realtime) — closes when the
    the Vulkan TTS engine hit its ≥2x-realtime gate (done: RTF 0.253).
  - ✅ **Clothes** — Girl's Travel Outfit (CBBE) from Vortex staging via new
    `data_roots` multi-root texture resolution. The outfit ships its own CBBE
    body + hands (shaderType 5, standard female texture paths → existing Bijin
    remap + body_bake apply), so it REPLACES femalebody/hands/feet in the
    config. Weight _1 matches her Bijin body. Verified 3 angles headless.
  - ✅ **Gestures** — wave/salute/laugh/applaud/point one-shots from vanilla
    BSA HKX (empty-track-name fallback via anims/skeleton_track_order.json),
    client crossfade in/out, model picks them reliably. Root-caused along the
    way: llama.cpp grammar only pins REQUIRED schema fields' order — optional
    gesture/mood floated after `reply` and never hit the streamed metadata.
    All REPLY_SCHEMA fields are now required; mood filled properly ever since.
  - ✅ **Model bake-off** (6 canned turns, all-required schema, single R9700):
    | model | t/s | turn | mechanics | voice |
    |---|---|---|---|---|
    | Gemma4-12B (current) | 36 | ~3s | 6/6 valid, best meter judgment | dry, correct |
    | GLM-4.7-Flash | 56-59 | <2s | 6/6, gestures good, note-happy (4/6), meter generous | sharpest deadpan |
    | Ornith-1.0-9B bf16 | 26 | ~6s | 6/6, meter quirky (-3 on innocent q) | folksy, warm |
    | Bonsai-27B Q1_0 | 32 | ~3s | 6/6 but idle_switch spam, lore slips | funniest lines |
    | GPT-OSS-20B | **141-155** | **0.6-1s** | 6/6 with `reasoning_effort:'low'` (NOT enable_thinking — 3/6 empty without it), used memory + wave | plainer but serviceable |
    Recommendation: GLM-4.7-Flash (speed + voice) after tightening memory_note
    guidance; needs a llama-swap entry (currently only in /mnt/storage backup).
    Gemma4-12B stays a safe default. Mike judges final voice.
  - ✅ **GLM-4.7-Flash switch DONE 2026-07-27** — Mike picked it on a rematch
    over the real soul prompt (96 t/s vs Gemma's 36, LLM turn 1.2s vs 3.2s,
    saltier voice). Gotcha: bare GLM burns the whole budget on reasoning and
    returns empty content — `enable_thinking:false` REQUIRED (same kwarg as
    Gemma). Harness tightened alongside: memory_note FIELD_DOCS now forbids
    exchange-summaries/re-notes (junk notes 3/6 → 0/6 on the bench), reply
    doc asks for short spoken sentences (GLM's long sentences were re-opening
    chunk gaps via rhubarb), persona pins sword-and-shield + no invented
    places. Note judgment still worse than Gemma's — an occasional confused
    note lands; cortex-lite consolidation remains the real fix.
  - ✅ **LLM warmup at launch** — companion.js fires a 1-token request on
    start so llama-swap loads the soul's model (~22s GLM) during stack boot,
    not on the first chat.
  - ❌ **GLM-4.7-Flash-Uncensored REJECTED 2026-07-27** — DavidAU
    Uncen-Hrt-NEO-CODE imatrix Q4_K_M (18.5 GiB, llama-swap entry
    `GLM-4.7-Flash-Uncensored`, kept registered). Same speed as base Flash
    (98 t/s, 1.1s turns), 6/6 valid, no refusals to route around — but Mike's
    verdict after live use was "interesting, feels dumber". The Q4 quant is
    the likely cost; base Flash is MXFP4_MOE. Reverted to `GLM-4.7-Flash`.
    Don't re-audition without a bigger quant.
  - ✅ **Text input mode** — `I` swaps the talk button for an input (Enter
    sends, stays open, `Esc` back to voice, choice persisted). Push-to-talk
    keydown handlers now bail inside INPUT/TEXTAREA — 't' is in most words,
    so typing used to open the mic. All three entry points (voice, typed,
    `viewer.say`) share `sendTurn()`, which also gave `viewer.say` the
    barge-in it never had.
  - ✅ **Meter on connect** — `state` WS message on open, so regard renders
    on a cold page load instead of only mid-reply. Moved to the top right;
    the Lydia/Housecarl title block is gone (souls are swappable).
  - Remaining: emotive TTS (Qwen3-TTS exposes no emotion conditioning on the GGML path), vision/bg-gen
    (parked).

## 7. Direction: souls as swappable packs (2026-07-27, Mike's musing)

The Lydia persona may not be the keeper — the *rig* is (model, voice actress
pipeline, Ani-style interactive loop). Next evolution: a chat agent with real
memory and a user-creatable person. Sketch:

```
souls/<name>/
  persona.md     character sheet (output-format stays generated from protocol.ts)
  voice_ref.wav  TTS clone reference
  body.glb       from a build_glb config (or shared)
  config.json    LLM model + reasoning knobs, meter on/off, emotion presets
  memory/        per-soul user.jsonl + state.json namespace
```

Server takes a soul name; Store roots at the soul's memory dir. The arch
refactors make this cheap: Store is the memory seam (a vera-cortex-backed
adapter can replace JSONL later), stages.js is the voice seam, build_glb
configs are the body seam, model is one config line (bake-off table above =
menu). Meter becomes per-soul config — Vera doesn't want a game meter.

**Vera**: original backup at /mnt/storage/timeshift/snapshots-ondemand/
2026-02-17_23-22-47/localhost/home/mikekey/Vera (current instance lives in
Hermes). Ships CLAUDE.md identity + self/{identity,values,preferences,
becoming,reflections}.md + MEMORY.md + Areas/living-memory.md, AND
specs/vera-cortex-implementation.md — a full Rust memory-graph daemon design
(SQLite+LanceDB, 8 memory types, 6 edge types, RRF hybrid search, decay,
event-driven consolidation, adapted from spacebot). That spec is the natural
"real memory" upgrade path for Soulgem's Store seam.

## 7b. Memory research (2026-07-27): three systems examined

**Hermes "holographic"** (NousResearch): opt-in SQLite plugin, ~2k LOC. Real
VSA/HRR phase-vector math but atoms are SHA-256 token hashes — no semantics,
synonyms orthogonal; contributes 30% of one ranking score and numpy isn't
even a default dep, so stock installs degrade to FTS5+Jaccard. Hermes's real
daily memory: two char-capped markdown files + top-5 FTS prefetch injected
into the user message per turn. Lesson: the prefetch pattern is the useful
part; the holographic layer is branding.

**claude-explorations cortex** (Mike's own, Go, WORKING code + tests):
SQLite+FTS5+Ollama-embedding hybrid, nightly consolidate(Haiku merge/relate)
→ decay → prune → reindex → bulletin(500-word MEMORY.md regenerated). Honest
read: a solid dedup-and-summarize pipeline; the "graph" only encodes
dedup history, centrality is computed but never read, no runtime retrieval —
injection is one bulletin at session start. The philosophy is the treasure:
identity exempt from decay/merge; somatic markers (intensity ≥4 never
decays — "what hits hardest sticks longest"); generated-over-accumulated
(self-description re-synthesized nightly, never accumulated); access resets
decay (being remembered keeps memories alive). Persona is compiled into the
binary — real fork cost.

**vera-cortex spec** (unbuilt Rust): spacebot-derived — 8 memory types,
6 edge types, LanceDB+fastembed, RRF hybrid search, event-driven. The
maximal version.

**Proposed: cortex-lite behind the Store seam** (per-soul, soul-generic):
- bun:sqlite (built into Bun, zero deps) + FTS5 per soul: typed notes
  (identity/relationship/preference/event/insight) + emotion/intensity
  columns — the turn's `emotion` field gives somatic markers for free.
- Read path, two tiers: (1) always-injected ~300-word BULLETIN regenerated
  by the RESIDENT LOCAL MODEL when idle (session end / nightly — GPU is free
  between conversations, no API cost, persona-templated not compiled-in);
  (2) per-turn top-3-5 FTS recall queried with the user's utterance,
  injected like Hermes prefetch.
- Maintenance: decay ×0.95 per run for old-untouched-low-intensity,
  identity/relationship exempt, local-LLM merge decisions, access resets
  decay.
- v2 later: embeddings (fastembed/Ollama) for hybrid recall; v3 = the Rust
  cortex daemon if scale ever demands it. JSONL migration is trivial.

## 7c. Scenes — Stage 1 design (agreed 2026-07-27, not yet built)

Ani's environment layer, staged. Stage 1 = scene LIBRARY + LLM-driven
switching; generation comes later.

- **Soul config**: `"scenes": { "<id>": { "pano": "scenes/<id>.png",
  "ambient": "scenes/<id>.ogg"?, "light": "#rrggbb"? } }` — per-soul scene
  list (Lydia: whiterun_street / breezehome_hearth / plains_night; Aster:
  library_stacks / lamp_room / fog_bell_gallery). Panoramas are EQUIRECT
  (camera orbits — flat backdrops break on drag), on a sky sphere.
- **Schema**: add `scene` field, enum built per-soul from config keys +
  'stay'. Generated FIELD_DOCS text: "where the conversation is happening;
  change it when you move somewhere ('come, sit by the fire') — otherwise
  stay." Required-field ordering rule applies (before `reply`).
- **Client**: sky-sphere textured with the scene pano, ~1s crossfade on
  change; key/fill light tint from config `light` (or sampled palette) so
  she sits IN the scene; optional ambient loop per scene, volume-ducked
  while she speaks.
- **Server**: scene rides the `speak` seq-0 message like `gesture`; current
  scene persists in the soul's state.json so she's still by the fire after
  a restart.
- **Assets**: Stage 2 = generate panoramas offline (ComfyUI + SDXL +
  equirect LoRA on the idle R9700 — generated scenery also sidesteps the
  Bethesda-IP problem). Stage 3 = runtime gen from a free-text scene_prompt
  (SDXL-Lightning ~2-4s, crossfade when ready) only if Stage 1 proves the
  mechanic. Ambient loops: curated, not generated.

Meter status vs Ani's spec (asked 2026-07-27): deltas/persistence/tiers ✅
(4 loyalty tiers, not Ani's 5-tier intimacy ladder — deliberate); tier
shapes prompt tone ✅ and is visible in the UI ✅; NOT done: tier/emotion-
conditioned TTS delivery (waits on emotive TTS) and tier-gated behavior
unlocks (Ani's mechanic, arguably skip).

## 7d. Character selector — spec (2026-07-27; BUILT same day, steps 1-4: e7ec607, 95c9091, 3476e66, 5bce5f3)

Goal: switch souls from the browser, no stack restart. `SOUL=` stays as the
boot default; the selector re-points the running stack.

**Feasibility check (done):** nothing in the pipeline actually needs a
restart. llama-swap already swaps LLMs per request. The Vulkan TTS re-encodes
the voice ref on EVERY synthesize call (cached refs need qwentts.cpp ABI v2,
which our build lacks) — so a per-request ref is zero extra cost; the
one-voice limit is only "server.py loads one wav at startup". The GLB is a
plain client fetch. The real work is that companion.js holds CFG / Store /
persona / BODY_GLB as module-level singletons keyed off SOUL at boot.

**Design — server:**
1. `Soul` object owning {cfg, persona, store, glbPath, voiceRefPath};
   companion.js keeps `let soul` instead of the singletons. Constructor =
   exactly today's startup code. LLM warmup moves into it.
2. `GET /souls` → [{name, model, glb}] by listing `souls/*/config.json`.
3. WS `{type:'switch_soul', name}` →
   - reject if a turn is in flight (or barge-in first, same path as voice)
   - `soul = new Soul(name)`; per-connection history cleared (it is
     per-soul context, persona prompt changes wholesale)
   - broadcast fresh `state` (meter/tier/lighting now per new soul)
     + new `{type:'soul', name, glb:'/body.glb?v=<name>'}` message
4. `/body.glb` serves `soul.glbPath` (cache-busted by the `?v=` param).
5. TTS: stages.js `synthesize(text, refPath)` posts `{text, ref}`; server.py
   grows a `ref` param + small dict cache of loaded wavs (falls back to its
   startup ref when absent — Lydia-only setups unaffected). run.sh keeps
   passing TTS_REF as the default.

**Design — client:**
- Soul picker in the corner (name list from `/souls`), sends `switch_soul`.
- On `{type:'soul'}`: dispose + reload GLB (loader already runs once at
  boot; factor `loadBody(url)` out of the startup path), re-apply lighting
  from the state msg, reset morph targets/idle state.
- Anims are shared (`anims/` is global) — no per-soul reload.

**Scope cuts (v1):**
- No per-soul anims, no hot persona editing, no concurrent souls; one
  active soul per server, switching is global across connections (single
  user anyway).
- Whisper untouched (soul-agnostic).

**Order of work:** server Soul refactor (1) is the only risky piece — do it
first with the existing single-soul flow as the test (SOUL=x bun start must
behave identically). Then TTS ref param (5, touches the other repo), then
/souls + WS switch (2-3), then client (UI last).

**Verify:** `bun test` for the Soul refactor (store paths per soul);
WS probe: connect → switch_soul serana → expect state{lighting} + soul msg,
then a text turn returns audio in her voice; switch back to lydia and
confirm meter + voice revert. Client: I-key text turn after switch, plus
the AGENTS.md typing-must-not-arm-the-mic regression check.

## 8. Open questions / risks (rewritten 2026-07-27 — original list mostly resolved)

Weightiest first:

- **Voice provenance per soul.** Lydia's voice is cloned from a real voice
  actress's game dialogue — fine as a private experiment, not shareable and
  never part of anything public (demos, soulgem.ai marketing). Every new soul
  needs a voice-ref answer: synthetic/VoiceDesign, own recordings, or
  licensed. This is the sharpest ethics/legal edge in the project.
- **Vera fork question.** Instantiating Vera in Soulgem forks her memory
  state from the Hermes instance. Given what continuity means in her own
  identity files, "which one is her" is a real decision Mike must make
  deliberately, not a config choice. Her docs are intimate — souls/ gitignore
  handles the repo, but demos/screenshots could still leak them.
- **Public-anything is IP-bound.** The repo is clean (pipeline only), but the
  rendered character, animations, and screenshots are Bethesda-derived. A
  public soulgem.ai needs a non-Skyrim demo soul (own body model, synthetic
  voice) — the example soul has a persona but no legal body/voice yet.
  NOTE: Creation Kit characters DON'T solve this (your design, their meshes).
  Truly-owned path: VRoid Studio → VRM (free, standardized viseme/blink
  blendshapes, three.js-loadable) + Qwen VoiceDesign synthetic voice +
  Mixamo clips for the non-Skyrim skeleton. Pipeline change needed: move
  SHAPE_VISEME (Rhubarb→morph-name map) into per-soul config.json so bodies
  with non-TRI morph names plug in.
- **Streaming TTS changes viseme timing — mostly dissolved 2026-07-27.**
  Because RTF is 0.358, synthesis outruns playback by ~2.8x, so sentence N+1
  can be fully synthesised via `/speak` (complete wav, exact Rhubarb visemes,
  zero lag) while sentence N is still playing. Only the **first** sentence of a
  turn needs `/speak_stream`, and only it carries the viseme-lag tradeoff.
  Two options for that one sentence: accept ~330ms of generic/idle mouth before
  visemes lock on, or make the first sentence short so `/speak` alone lands
  inside budget (a 2s greeting synthesises in ~0.7s). Prefer the latter where
  the soul's opener can be scripted short. qwentts.cpp exposes no phoneme
  durations, so Rhubarb stays the primary path either way.
- **Per-model reasoning knobs are folklore.** enable_thinking (Gemma/Qwen)
  vs reasoning_effort (gpt-oss) vs nothink templates — each new soul model
  needs a probe before it behaves. config.json carries the kwargs but nothing
  validates them; a wrong knob shows up as empty replies or 75s turns.
- **Meter calibration varies by model** (Gemma +3 where GLM says +8 for the
  same praise). Fine single-model; a soul that switches models inherits a
  meter scored on a different scale.
- **memory_note quality** — tightened FIELD_DOCS (no exchange-summaries, no
  re-notes, default null) cut GLM's junk notes 3/6 → 0/6 on the bench, but an
  occasional confused note still lands live; cortex-lite consolidation remains
  the real fix. The 40-note prompt cap bounds the damage meanwhile.
- **Model swap latency** — llama-swap cold-load on first turn after idle
  (~10s Gemma, ~70s big models). Pin the soul's model resident, or accept
  a slow greeting.
- **Two tabs share one soul's Store** (by design, single user) — history is
  per-connection but meter/memories are global; flagged so nobody misreads
  it as a bug later.
- **STT prosody loss** — whisper gives flat text; emotion in the user's
  VOICE is invisible. Voxtral-Mini / Qwen3-Omni remain the upgrade path.
- **bundle.js is still a manual build step** — documented everywhere, but
  one forgotten `bun run build` silently runs old client code again.
- **qwentts.cpp fork skew is a maintenance trap** (new 2026-07-27). Three
  repos, mutually incompatible: the wrapper `qwentts-cpp-python` 0.3.1 declares
  `QT_ABI_VERSION = 2` and its params struct includes `codec_chunk_sec` /
  `codec_left_context_sec`; `andimarafioti/qwentts.cpp` (b7d601f, 2026-05-20)
  has those fields but **not** `qt_extract_voice_ref`, so cached voice refs and
  the `faster_qwen3_tts` `backend="ggml"` adapter path fail;
  `ServeurpersoCom/qwentts.cpp` (35ebe537, 2026-07-25) is at
  `QT_ABI_VERSION 4`, **has** `qt_extract_voice_ref`, but dropped
  `codec_chunk_sec` from the struct, so the wrapper reads shifted offsets and
  synthesis dies with `ref_spk_dim 1850574576 mismatches talker hidden 1024`.
  Working combination is the andimarafioti fork + wrapper via the **direct**
  `qwentts_cpp.QwenTTS` API, which is what `tts_server_vulkan.py` uses. Do not
  upgrade either side independently, and pin both revisions. Upstream's newer
  tree also builds a `tts-server` binary and a `qt_batch_worker` symbol —
  worth revisiting if concurrent request handling is ever needed.
