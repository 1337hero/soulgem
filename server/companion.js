// Lydia companion orchestrator: static viewer + WS voice loop.
//   bun run server/companion.js
// Pipeline per turn: audio -> whisper-server -> GLM (persona, JSON) -> TTS -> client.
import { dirname, join } from 'path'
import { REPLY_SCHEMA, EMOTION_MOOD, outputFormatDoc } from './protocol.ts'
import { replyParser } from './reply_stream.ts'
import { transcribe, synthesize, lipSync } from './stages.js'
import { Store, tierOf } from './store.ts'

const ROOT = dirname(import.meta.dir)  // project root
const PORT = 8471
const LLM_URL = 'http://127.0.0.1:8082/v1/chat/completions'

// ---- soul pack: persona + config + memory namespace (SOUL env selects) ----
const SOUL = Bun.env.SOUL ?? 'lydia'
const SOUL_DIR = join(ROOT, 'souls', SOUL)
if (!(await Bun.file(join(SOUL_DIR, 'persona.md')).exists())) {
  console.error(`no such soul: souls/${SOUL}/ (try SOUL=example)`)
  process.exit(1)
}
const CFG = await Bun.file(join(SOUL_DIR, 'config.json')).json()
const PERSONA = await Bun.file(join(SOUL_DIR, 'persona.md')).text()
const BODY_GLB = join(ROOT, CFG.glb ?? 'lydia.glb')

const store = new Store(join(SOUL_DIR, 'memory'), { meter: CFG.meter !== false })

const systemPrompt = () => PERSONA + '\n' + outputFormatDoc() + store.promptSection()

function applyThought(parsed) {
  if (typeof parsed.meter_delta === 'number') store.applyMeterDelta(parsed.meter_delta)
  if (parsed.memory_note) store.addNote(parsed.memory_note)
}

// Per-connection conversation history, trimmed to the last HISTORY_CAP turns.
const HISTORY_CAP = 48  // ponytail: hard cap; nothing to configure yet
function push(history, msg) {
  history.push(msg)
  if (history.length > HISTORY_CAP) history.splice(0, history.length - HISTORY_CAP)
}

// Streams the LLM response, yielding events as they become available:
//   {type:'meta', meta}      — emotion/mood/gesture/... (before reply text starts)
//   {type:'sentence', text}  — each completed sentence of the reply
//   {type:'done', thought}   — full parsed object at the end
async function* thinkStream(userText, token, history) {
  push(history, { role: 'user', content: userText })
  const res = await fetch(LLM_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model: CFG.model,
      messages: [{ role: 'system', content: systemPrompt() }, ...history.slice(-24)],
      temperature: 0.8,
      max_tokens: 400,
      stream: true,
      chat_template_kwargs: CFG.chat_template_kwargs ?? {},
      response_format: {
        type: 'json_schema',
        json_schema: { name: 'lydia_reply', schema: REPLY_SCHEMA },
      },
    }),
  })
  if (!res.ok) throw new Error(`llm ${res.status}: ${await res.text()}`)

  const parser = replyParser()
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let sse = ''
  try {
    while (true) {
      if (token.cancelled) return
      const { done, value } = await reader.read()
      if (done) break
      sse += dec.decode(value, { stream: true })
      const lines = sse.split('\n')
      sse = lines.pop()
      for (const line of lines) {
        if (!line.startsWith('data: ') || line === 'data: [DONE]') continue
        const delta = JSON.parse(line.slice(6)).choices?.[0]?.delta?.content
        if (delta) yield* parser.push(delta)
      }
    }
  } finally {
    reader.cancel().catch(() => {})
  }
  if (token.cancelled) return

  for (const ev of parser.finish()) {
    if (ev.type === 'done') {
      push(history, { role: 'assistant', content: ev.thought.reply })
      applyThought(ev.thought)
    }
    yield ev
  }
}

const activeTurn = new WeakMap()  // ws -> cancellation token for the in-flight turn

async function handleTurn(ws, userText) {
  activeTurn.get(ws) && (activeTurn.get(ws).cancelled = true)  // barge-in via new turn
  const token = { cancelled: false }
  activeTurn.set(ws, token)

  const t0 = Date.now()
  ws.send(JSON.stringify({ type: 'transcript', text: userText }))
  let meta = null
  let seq = 0
  let tFirst = 0
  let stageError = null          // first synth/lipsync failure; rethrown after the chain drains
  let interrupted = false        // log barge-in once, not per pending chunk
  let sendChain = Promise.resolve()  // serializes speak messages in seq order

  for await (const ev of thinkStream(userText, token, ws.data.history)) {
    if (ev.type === 'meta') {
      meta = ev.meta
    } else if (ev.type === 'sentence') {
      const mySeq = seq++
      // Start synth+lipsync NOW — sentence N+1 must not wait for sentence N's
      // rhubarb (pocketSphinx is ~RTF 0.9 on one core, but 8 concurrent cost
      // barely more than 1). Only the send order is serialized, via sendChain.
      const job = (async () => {
        if (token.cancelled) return null
        const wav = await synthesize(ev.text)
        if (token.cancelled) return null
        return { wav, visemes: await lipSync(wav, ev.text) }
      })()
      sendChain = sendChain.then(async () => {
        let ready
        try {
          ready = await job
        } catch (e) {
          stageError ??= e
          token.cancelled = true   // abandon the rest of the turn
          return
        }
        if (!ready || token.cancelled) {
          if (!interrupted) console.log(`turn interrupted at chunk ${mySeq} — "${ev.text.slice(0, 40)}"`)
          interrupted = true
          return
        }
        const mood = Object.keys(meta?.mood ?? {}).length ? meta.mood
                   : EMOTION_MOOD[meta?.emotion] ?? {}
        tFirst ||= Date.now()
        ws.send(JSON.stringify({
          type: 'speak', seq: mySeq, sentence: ev.text,
          emotion: meta?.emotion, mood, gesture: mySeq === 0 ? meta?.gesture ?? 'none' : 'none',
          meter: store.meter, tier: tierOf(store.meter)[1],
          visemes: ready.visemes, audio: ready.wav.toString('base64'),
        }))
      })
    } else if (ev.type === 'done') {
      await sendChain
      if (stageError) throw stageError
      if (token.cancelled) return
      ws.send(JSON.stringify({ type: 'speak_end' }))
      console.log(`turn: first audio ${tFirst - t0}ms, total ${Date.now() - t0}ms, ${seq} chunk(s) — "${ev.thought.reply.slice(0, 60)}"`)
    }
  }
  await sendChain  // cancelled mid-stream: let queued (skipping) links settle
  if (stageError) throw stageError
}

const MIME = { html: 'text/html', js: 'text/javascript', json: 'application/json',
               glb: 'model/gltf-binary', png: 'image/png', css: 'text/css' }

Bun.serve({
  port: PORT,
  idleTimeout: 120,
  async fetch(req, server) {
    const url = new URL(req.url)
    if (url.pathname === '/ws') {
      return server.upgrade(req, { data: { history: [] } })
        ? undefined : new Response('upgrade failed', { status: 400 })
    }
    let path = url.pathname === '/' ? '/index.html' : url.pathname
    const file = path === '/body.glb' ? Bun.file(BODY_GLB) : Bun.file(join(ROOT, path.slice(1)))
    if (!(await file.exists())) return new Response('not found', { status: 404 })
    const ext = path.split('.').pop()
    return new Response(file, { headers: { 'Content-Type': MIME[ext] ?? 'application/octet-stream' } })
  },
  websocket: {
    maxPayloadLength: 32 * 1024 * 1024,
    open(ws) {
      // meter otherwise only rides the seq-0 speak chunk, so a fresh page shows
      // nothing until she talks. Null when the soul runs meterless.
      const on = CFG.meter !== false
      ws.send(JSON.stringify({
        type: 'state',
        meter: on ? store.meter : null,
        tier: on ? tierOf(store.meter)[1] : null,
        lighting: CFG.lighting ?? null,
      }))
    },
    async message(ws, raw) {
      try {
        const msg = JSON.parse(raw)
        if (msg.type === 'audio') {
          const bytes = Buffer.from(msg.data, 'base64')
          const text = await transcribe(bytes)
          if (!text || text.length < 2) {
            ws.send(JSON.stringify({ type: 'error', error: 'heard nothing' }))
            return
          }
          await handleTurn(ws, text)
        } else if (msg.type === 'text') {
          await handleTurn(ws, msg.text)
        } else if (msg.type === 'interrupt') {
          const token = activeTurn.get(ws)
          if (token) token.cancelled = true
        }
      } catch (e) {
        console.error(e)
        ws.send(JSON.stringify({ type: 'error', error: String(e.message ?? e) }))
      }
    },
  },
})

console.log(`lydia companion on http://localhost:${PORT}`)

// Wake llama-swap now so the soul's model loads at launch, not on first chat
// (~10s Gemma, ~70s big models). Fire-and-forget; first turn just gets faster.
fetch(LLM_URL, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ model: CFG.model, messages: [{ role: 'user', content: 'wake' }], max_tokens: 1 }),
}).then(r => console.log(`llm ${CFG.model} ${r.ok ? 'warm' : `warmup failed: ${r.status}`}`),
        e => console.log(`llm warmup failed: ${e.message ?? e}`))
