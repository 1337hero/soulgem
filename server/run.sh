#!/usr/bin/env bash
# Start the Soulgem companion stack: whisper ASR + Qwen TTS + orchestrator.
# LLM is assumed already running via llama-swap on :8082.
# SOUL=<name> selects souls/<name>/ (default lydia); its config.json may set
# voice_ref for the TTS clone.
set -e
cd "$(dirname "$0")/.."

export SOUL="${SOUL:-lydia}"
VOICE_REF=$(bun -e "console.log(JSON.parse(await Bun.file('souls/$SOUL/config.json').text()).voice_ref ?? '')")

WHISPER_BIN=~/Experiments/voice/whisper.cpp/build-vulkan/bin/whisper-server
WHISPER_MODEL=~/.local/share/whisper/models/ggml-large-v3-turbo.bin

"$WHISPER_BIN" -m "$WHISPER_MODEL" --host 127.0.0.1 --port 8124 &
WHISPER_PID=$!

TTS_REF="$VOICE_REF" ~/Experiments/voice/qwen-tts-env/bin/python3.12 server/tts_server.py &
TTS_PID=$!

trap 'kill $WHISPER_PID $TTS_PID 2>/dev/null' EXIT

bun run server/companion.js
