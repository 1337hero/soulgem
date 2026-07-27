#!/usr/bin/env python3
"""Lydia TTS server — Qwen3-TTS voice clone behind a tiny HTTP API.

Run with the qwen-tts venv:
  ~/Experiments/voice/qwen-tts-env/bin/python3.12 server/tts_server.py

POST /speak  {"text": "..."}  ->  audio/wav
GET  /health                  ->  {"ok": true, "model": "..."}
"""
import io
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy as np
import soundfile as sf

PORT = int(os.environ.get('TTS_PORT', 8123))
MODEL_NAME = os.environ.get('TTS_MODEL', 'Qwen/Qwen3-TTS-12Hz-0.6B-Base')
REF_AUDIO = os.path.expanduser('~/Experiments/voice/qwen-tts-env/voice_ref_60s.wav')

os.environ.setdefault('HIP_VISIBLE_DEVICES', '2')
# Every novel sentence length is a new tensor shape; default MIOpen find mode
# re-searches conv solutions per shape (~4x RTF). FAST uses heuristics instead.
os.environ.setdefault('MIOPEN_FIND_MODE', 'FAST')

print(f'loading {MODEL_NAME}...', file=sys.stderr)
t0 = time.time()
import torch
from qwen_tts import Qwen3TTSModel
# bfloat16, not float16: fp16 overflows in the talker sampling path on gfx1201,
# producing NaN probs that trip a device-side assert inside multinomial.
model = Qwen3TTSModel.from_pretrained(MODEL_NAME, device_map='cuda:0', dtype=torch.bfloat16)
prompt = model.create_voice_clone_prompt(ref_audio=REF_AUDIO, x_vector_only_mode=True)
print(f'ready in {time.time()-t0:.1f}s on port {PORT}', file=sys.stderr)

# warmup (first generation compiles kernels)
model.generate_voice_clone(text='Ready.', language='English',
                           voice_clone_prompt=prompt, x_vector_only_mode=True)
print('warmed up', file=sys.stderr)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print(f'[tts] {fmt % args}', file=sys.stderr)

    def do_GET(self):
        if self.path == '/health':
            body = json.dumps({'ok': True, 'model': MODEL_NAME}).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        if self.path != '/speak':
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get('Content-Length', 0))
        req = json.loads(self.rfile.read(length))
        text = req['text'].strip()
        t0 = time.time()
        audio_list, sr = model.generate_voice_clone(
            text=text, language='English',
            voice_clone_prompt=prompt, x_vector_only_mode=True)
        audio = np.asarray(audio_list[0], dtype=np.float32)
        buf = io.BytesIO()
        sf.write(buf, audio, sr, format='WAV')
        wav = buf.getvalue()
        dur = len(audio) / sr
        gen = time.time() - t0
        print(f'[tts] {len(text)} chars -> {dur:.1f}s audio in {gen:.2f}s '
              f'(RTF {gen/dur:.2f})', file=sys.stderr)
        self.send_response(200)
        self.send_header('Content-Type', 'audio/wav')
        self.send_header('Content-Length', str(len(wav)))
        self.end_headers()
        self.wfile.write(wav)


ThreadingHTTPServer(('127.0.0.1', PORT), Handler).serve_forever()
