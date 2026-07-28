#!/usr/bin/env bash
# Start the Soulgem companion stack: whisper ASR + Qwen TTS + orchestrator.
# LLM is assumed already running via llama-swap on :8082.
# SOUL=<name> selects souls/<name>/ (default lydia); its config.json may set
# voice_ref for the TTS clone.
set -e
cd "$(dirname "$0")/.."

export SOUL="${SOUL:-lydia}"
VOICE_REF=$(bun -e "console.log(JSON.parse(await Bun.file('souls/$SOUL/config.json').text()).voice_ref ?? '')")

# Machine-specific paths, overridable so this runs somewhere other than ArchBox.
WHISPER_BIN="${WHISPER_BIN:-$HOME/Experiments/voice/whisper.cpp/build-vulkan/bin/whisper-server}"
WHISPER_MODEL="${WHISPER_MODEL:-$HOME/.local/share/whisper/models/ggml-large-v3-turbo.bin}"

# Build before launching anything: a compile error under `set -e` must not leave
# a half-started stack behind.
mkdir -p bin
go build -o bin/tts-server ./cmd/tts-server

CHILDREN=()
cleanup() {
  [[ ${#CHILDREN[@]} -gt 0 ]] && kill "${CHILDREN[@]}" 2>/dev/null
  return 0
}
trap cleanup EXIT

"$WHISPER_BIN" -m "$WHISPER_MODEL" --host 127.0.0.1 --port 8124 &
CHILDREN+=($!)

# The Go server binds directly to qwentts.cpp's C ABI; no Python runtime.
TTS_REF="$VOICE_REF" bin/tts-server &
CHILDREN+=($!)

bun run server/companion.js
