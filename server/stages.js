// Pipeline stage adapters: ASR, TTS, lip-sync. Each is a narrow async function so
// a different service (streaming TTS fork, Voxtral ASR) is a swap here, not in the
// orchestrator. Service endpoints are env-overridable.
import { $ } from 'bun'
import { join } from 'path'
import { SHAPE_VISEME } from './protocol.ts'

const WHISPER_URL = Bun.env.LYDIA_ASR_URL ?? 'http://127.0.0.1:8124/inference'
const TTS_URL = Bun.env.LYDIA_TTS_URL ?? 'http://127.0.0.1:8123/speak'
const RHUBARB = Bun.env.LYDIA_RHUBARB ?? join(import.meta.dir, 'rhubarb/rhubarb')
// pocketSphinx (default) uses the dialog text; ~2.3s init + 0.79x audio, one
// core, no daemon mode — slow serially, but 8 concurrent invocations cost
// barely more than 1, so companion.js pipelines it off the critical path.
// 'phonetic' is acoustic-only (ignores the transcript) and ~5x faster if the
// first-sentence latency ever needs it.
// phonetic: ~5x faster than pocketSphinx, keeps the first sentence off a
// multi-second critical path. pocketSphinx mouths slightly better but costs
// 2.3s init + 0.79x audio on EVERY first chunk (Mike accepted the trade).
const RECOGNIZER = Bun.env.LYDIA_RHUBARB_RECOGNIZER ?? 'phonetic'

// webm/opus bytes -> text
export async function transcribe(audioBytes) {
  const tmp = `/tmp/lydia-utt-${crypto.randomUUID()}`
  try {
    await Bun.write(tmp + '.webm', audioBytes)
    await $`ffmpeg -y -loglevel error -i ${tmp + '.webm'} -ar 16000 -ac 1 ${tmp + '.wav'}`
    const form = new FormData()
    form.append('file', Bun.file(tmp + '.wav'))
    form.append('response_format', 'json')
    const res = await fetch(WHISPER_URL, { method: 'POST', body: form })
    if (!res.ok) throw new Error(`whisper ${res.status}`)
    return (await res.json()).text.trim()
  } finally {
    await $`rm -f ${tmp + '.webm'} ${tmp + '.wav'}`.quiet()
  }
}

// text -> wav bytes
// ref: absolute path to a 24 kHz mono clone wav; omit for the TTS server's
// startup default. The engine re-encodes the ref every call, so per-request
// voices cost nothing extra.
export async function synthesize(text, ref) {
  const res = await fetch(TTS_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(ref ? { text, ref } : { text }),
  })
  if (!res.ok) throw new Error(`tts ${res.status}`)
  return Buffer.from(await res.arrayBuffer())
}

// wav bytes + text -> viseme cues, or null to fall back to jaw-flap
export async function lipSync(wav, text) {
  const tmp = `/tmp/lydia-lip-${crypto.randomUUID()}`
  try {
    await Bun.write(tmp + '.wav', wav)
    await Bun.write(tmp + '.txt', text)
    const out = await $`${RHUBARB} -f json --machineReadable -r ${RECOGNIZER} -d ${tmp + '.txt'} ${tmp + '.wav'}`.quiet()
    return JSON.parse(out.stdout.toString()).mouthCues
      .map(c => ({ s: c.start, e: c.end, v: SHAPE_VISEME[c.value] ?? null }))
  } catch (e) {
    console.error('rhubarb failed, falling back to jaw-flap:', e.message ?? e)
    return null
  } finally {
    await $`rm -f ${tmp + '.wav'} ${tmp + '.txt'}`.quiet()
  }
}
