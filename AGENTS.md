# AGENTS.md — working on Soulgem

Operational guide for agents (and future Mike). The architecture/state doc is
`COMPANION-PLAN.md`; this file is the how-to.

## Quickstart

```sh
bun start               # whisper :8124 + TTS :8123 + orchestrator :8471
SOUL=example bun start  # soul select (default lydia)
bun run build           # REQUIRED after editing main.js — index.html loads bundle.js
bun test                # server + client tests (temporary stores/services)
bun run check           # strict types + max-20 complexity + tests + client build
go test ./...           # Go parser/pipeline/TTS unit tests
```

llama-swap on :8082 is systemd-managed and assumed running. GPU status:
`rocm-smi` (never nvidia-smi). `HIP_VISIBLE_DEVICES`, never CUDA_.


## Characters (cmd/build-glb + internal/character/)

One config file per character: `internal/character/<name>.go` returns `Config`;
`go run ./cmd/build-glb <name>` builds it, no names = every character.

Everything else a character owns lives under `characters/<name>/`:

    characters/aster/
      aster.sha256                                  # build baseline (committed)
      aster.glb                                     # build output (gitignored)
      textures/actors/character/skin/…              # override DDS (gitignored)
      textures/actors/character/head/…

`characters/<name>` is the first entry in that character's `DataRoots`, so it
shadows every staging mod and the game Data dir. Because we own these paths,
every override is reached through an explicit `Remap` entry rather than by
happening to sit where a third-party mod put it. The DDS themselves are
game-derived and untracked — repopulate them by copying out of Vortex staging
(see "Swapping outfits") and confirm with `--verify`.

Note `characters/lydia/textures/…/skin/` is a byte-identical copy of Aster's:
they wear the same Bijin skin. That is deliberate duplication — it keeps the
characters independent, and git stores the identical blobs once anyway.
Characters must not affect each other — every optional capability is OFF
unless that character's config enables it:

- `skeleton` — vanilla female skeleton by default; set XPMSSE (staging) when
  an outfit is weighted to CBBE 3BA breast/butt bones (else those verts
  collapse — and see the NIF parser rule below before suspecting weights).
- `texture_bsas` — loose files only by default; list the game BSAs to let
  vanilla-only textures (mouth, vanilla outfits) resolve.
- per-mesh `MeshOptions{Skip: map[string]bool{"ShapeName": true}}` — drop one shape from a NIF (e.g. an
  outfit's cut-down body, or the Gauntlet from gloves).
- `BodyMatch` + `BodyFactors` — the match string picks which
  textures get the skin lift, so it never bleeds onto another character. It
  is a substring test against the *resolved* texture path, so keep it aimed
  at `characters/<name>/textures/` (which we own) and never at a third-party
  mod's folder name — matching on the latter both breaks when the mod moves
  and silently catches unrelated files that happen to sit in that folder.

**Baselines**: `go run ./cmd/build-glb --verify` rebuilds every character to a
scratch file and fails if any hash moved vs `characters/<name>/<name>.sha256`;
`--freeze <name>` blesses a deliberate look change. Run --verify after ANY
GLB builder/NIF parser edit — it's the proof Lydia didn't move.

## Swapping outfits (CBBE track)

Recipe proven with Girl's Travel Outfit (`internal/character/lydia.go`) and
Twilight Princess Armor Mashup (`internal/character/serana.go`):

1. **Locate the mod** — Vortex staging:
   `~/.config/steamtinkerlaunch/vortex/staging/skyrimse/mods/<Mod Name>/`.
   Mods do NOT need deploying into game Data — `data_roots` in the config
   resolves meshes/textures straight from staging.
2. **Inspect the NIFs**: `go run ./cmd/nif "<mod>/Meshes/.../torso_1.nif"`
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
   (`actors\character\female\femalebody_1.dds` etc.) are remapped to the
   character's own `characters/<name>/textures/` copy of the Bijin skin by the
   existing `remap` entries — this is what keeps body skin matching her face.
   `BodyFactors` apply to any texture whose path matches the config's
   `BodyMatch` string.
5. **Edit the character config** (`internal/character/<name>.go`): swap mesh entries
   (absolute paths into staging are fine), add the mod dir to `data_roots`.
6. **Build + verify**: `go run ./cmd/build-glb <name>` (needs the game
   install), then view — `bun start` and screenshot front/back/face, or
   serve statically. Check: neck/wrist seams, skin tone match, no floating
   old body parts. Then `go run ./cmd/build-glb --verify` for the others.

New character entirely: copy `internal/character/serana.go`, add its loader to
`internal/character/config.go` — facegen formid + skin
remaps + meshes + a `souls/<name>/` dir (see Souls). Extract the voice with
`go run ./cmd/voice-ref <voicetype> souls/<name>/voice_ref.wav` (any voice type in the
voice BSAs; UHDAP preferred automatically). Non-game bodies (VRM etc.)
are a planned separate track (plan §8, SHAPE_VISEME must move to soul
config first).

## Adding animations

Any HKX works (vanilla BSA included — empty track names fall back to
`anims/skeleton_track_order.json`):

```sh
go run ./cmd/bsa extract "<Data>/<Game> - Animations.bsa" \
  "meshes/actors/character/animations/<name>.hkx" /tmp/x.hkx
go run ./cmd/hkx-anim /tmp/x.hkx # decodes to anims/<name>.anim + reindexes
```

Naming controls behavior (client-side, by prefix):
- `gesture_*` — one-shot emotes, played on the model's `gesture` field
- `talk_*` — talking body language, random one per speech chunk
  (`talk_angry*` pool used on annoyed emotion)
- `dance_*` — a looping dance + its music track, played on `gesture: 'dance'`
  (see "Dance for me" below); never joins the idle rotation
- anything else — joins the random idle rotation (loopable clips only;
  female variants live under `animations/female/` in the BSA)

Rename by naming the extracted .hkx before decoding. `--reindex` rebuilds
the index alone. Keep clips ≥3s for idles; shorter one-shots are fine.
`--loop` trims each clip to one seamless loop (best pose match to frame 0
within 6–20s) — use it for full-length routines that would otherwise weigh
megabytes each (the Dance For Me dances run
the length of their song). hkxc renders blank track names as U+2400; the
parser strips it so those clips take the vanilla positional track-order
fallback (a regression there collapses every track into one bone).

## Dance for me

The "dance for me" button sends a hidden `Dance for me!` turn; she agrees in
her own words and the reply's `gesture: 'dance'` starts a looping `dance_*`
clip plus its music track (`music/dance_N.ogg`, ducked under her voice, capped
at `DANCE_MS`). Barge-in or the cap ends it. Baked offline from the Dance For
Me mod (`Dance for me - Dance for you SE(ESPfe)` in Vortex staging + the
`Dance4Me` music in game Data):

```sh
# animations: 4 spline HKX -> anims/dance_1..4.anim (looped, ~300-650KB each)
for n in 1 2 3 4; do cp "<staging>/.../animations/Dance19100$n/Dance19100${n}_S1.hkx" /tmp/dance_$n.hkx; done
go run ./cmd/hkx-anim --loop /tmp/dance_{1,2,3,4}.hkx
# music: xwm -> ogg (ffmpeg's wmapro, same path voice-ref uses)
mkdir -p music
for n in 1 2 3 4; do ffmpeg -v error -y -i "<Data>/music/Dance4Me/dance$n.xwm" -c:a libvorbis -q:a 5 music/dance_$n.ogg; done
```

Both `anims/*` and `music/` are gitignored (game-derived). `dance_N` clip
pairs with `dance_N.ogg` by index.

## Souls

`souls/<name>/{persona.md, config.json, memory/}` — copy `souls/aster/`.
config.json: `model` (llama-swap name), `chat_template_kwargs`, `voice_ref`
(TTS clone wav; omit = server default), `glb` (body, served as /body.glb),
`meter` (bool), `tiers` (optional [[min, name, prose], ...] — per-soul
relationship-tier voice; omit for the neutral default), `lighting` (optional per-soul light rig override: `exposure`
+ `ambient`/`key`/`fill`/`rim` each `{color, intensity}` — rides the state
msg, omitted fields keep the stock rig, so Lydia stays stock). Only
example/ is committed; souls are personal.

**Memory (cortex-lite):** `memory/cortex.db` (SQLite+FTS notes + MiniLM
vectors + cached bulletin) + `memory/state.json` (meter). Legacy
`user.jsonl` is migrated once on open then deleted. Per turn: hybrid recall
(FTS ∪ all-MiniLM-L6-v2 via llama-swap `MiniLM-L6`, RRF fused). After 60s
session idle: decay + dedupe + embed backfill + bulletin regen. Embed
endpoint: `LYDIA_EMBED_URL` / `LYDIA_EMBED_MODEL` (defaults
`http://127.0.0.1:8082/v1/embeddings`, `MiniLM-L6`). See `server/store.ts`,
`server/embed.ts`.

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
