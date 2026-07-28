// Lydia companion orchestrator: static viewer + WS voice loop.
//   bun run server/companion.js
// Pipeline per turn: audio -> whisper-server -> GLM (persona, JSON) -> TTS -> client.
import { existsSync, readdirSync, readFileSync } from 'fs'
import { dirname, join } from 'path'
import { REPLY_SCHEMA, EMOTION_MOOD, outputFormatDoc } from './protocol.ts'
import { replyParser } from './reply_stream.ts'
import { transcribe, synthesize, lipSync } from './stages.js'
import { Store, tierOf } from './store.ts'

const ROOT = dirname(import.meta.dir)  // project root
const PORT = 8471
const LLM_URL = 'http://127.0.0.1:8082/v1/chat/completions'

// ---- soul pack: persona + config + memory namespace (SOUL env selects the
// boot soul; everything soul-scoped hangs off `soul` so a switch is one swap) ----
async function loadSoul(name) {
  const dir = join(ROOT, 'souls', name)
  if (!(await Bun.file(join(dir, 'persona.md')).exists())) {
    throw new Error(`no such soul: souls/${name}/`)
  }
  const cfg = await Bun.file(join(dir, 'config.json')).json()
  const s = {
    name,
    cfg,
    persona: await Bun.file(join(dir, 'persona.md')).text(),
    glbPath: join(ROOT, cfg.glb ?? 'lydia.glb'),
    // absolute path for the per-request TTS clone ref; null = server default
    voiceRef: cfg.voice_ref
      ? (cfg.voice_ref.startsWith('~') ? cfg.voice_ref.replace('~', Bun.env.HOME)
                                       : join(ROOT, cfg.voice_ref))
      : null,
    store: new Store(join(dir, 'memory'), { meter: cfg.meter !== false, tiers: cfg.tiers }),
  }
  // Wake llama-swap now so the soul's model loads up front, not on first chat
  // (~10s Gemma, ~70s big models). Fire-and-forget; first turn just gets faster.
  fetch(LLM_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: cfg.model, messages: [{ role: 'user', content: 'wake' }], max_tokens: 1 }),
  }).then(r => console.log(`llm ${cfg.model} ${r.ok ? 'warm' : `warmup failed: ${r.status}`}`),
          e => console.log(`llm warmup failed: ${e.message ?? e}`))
  return s
}

let soul = await loadSoul(Bun.env.SOUL ?? 'lydia').catch(e => {
  console.error(`${e.message} (try SOUL=example)`)
  process.exit(1)
})

// every soul with both files is switchable; `active` marks the live one
function listSouls() {
  return readdirSync(join(ROOT, 'souls'), { withFileTypes: true })
    .filter(d => d.isDirectory()
      && existsSync(join(ROOT, 'souls', d.name, 'persona.md'))
      && existsSync(join(ROOT, 'souls', d.name, 'config.json')))
    .map(d => {
      const cfg = JSON.parse(readFileSync(join(ROOT, 'souls', d.name, 'config.json'), 'utf8'))
      return { name: d.name, model: cfg.model, glb: cfg.glb ?? 'lydia.glb', active: d.name === soul.name }
    })
}

const conns = new Set()  // live WS connections, for switch broadcasts

function sendState(ws) {
  // meter otherwise only rides the seq-0 speak chunk, so a fresh page shows
  // nothing until she talks. Null when the soul runs meterless.
  const on = soul.cfg.meter !== false
  ws.send(JSON.stringify({
    type: 'state',
    meter: on ? soul.store.meter : null,
    tier: on ? tierOf(soul.store.meter, soul.store.tiers)[1] : null,
    lighting: soul.cfg.lighting ?? null,
  }))
}

// Load first so a bad name changes nothing; then cancel in-flight turns
// (same path as barge-in), reset every connection's context, swap, announce.
async function switchSoul(name) {
  const next = await loadSoul(name)
  for (const c of conns) {
    const token = activeTurn.get(c)
    if (token) token.cancelled = true
    c.data.history.length = 0  // new persona = new conversation
  }
  soul = next
  console.log(`soul -> ${name}`)
  for (const c of conns) {
    sendState(c)
    c.send(JSON.stringify({ type: 'soul', name: soul.name, glb: `/body.glb?v=${soul.name}` }))
  }
}

const systemPrompt = () => soul.persona + '\n' + outputFormatDoc() + soul.store.promptSection()

function applyThought(parsed) {
  if (typeof parsed.meter_delta === 'number') soul.store.applyMeterDelta(parsed.meter_delta)
  if (parsed.memory_note) soul.store.addNote(parsed.memory_note)
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
      model: soul.cfg.model,
      messages: [{ role: 'system', content: systemPrompt() }, ...history.slice(-24)],
      temperature: 0.8,
      max_tokens: 400,
      stream: true,
      chat_template_kwargs: soul.cfg.chat_template_kwargs ?? {},
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
      // decoupled: audio ships the moment TTS finishes; the viseme track
      // chases it in a follow-up message (client jaw-flaps until it lands)
      const job = (async () => {
        if (token.cancelled) return null
        const wav = await synthesize(ev.text, soul.voiceRef)
        return token.cancelled ? null : { wav }
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
          meter: soul.store.meter, tier: tierOf(soul.store.meter, soul.store.tiers)[1],
          visemes: null, audio: ready.wav.toString('base64'),
        }))
        lipSync(ready.wav, ev.text).then(visemes => {
          if (visemes && !token.cancelled) {
            ws.send(JSON.stringify({ type: 'visemes', seq: mySeq, visemes }))
          }
        }).catch(() => {})  // no track = the chunk stays jaw-flapped
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
    if (url.pathname === '/souls') return Response.json(listSouls())
    let path = url.pathname === '/' ? '/index.html' : url.pathname
    const file = path === '/body.glb' ? Bun.file(soul.glbPath) : Bun.file(join(ROOT, path.slice(1)))
    if (!(await file.exists())) return new Response('not found', { status: 404 })
    const ext = path.split('.').pop()
    return new Response(file, { headers: { 'Content-Type': MIME[ext] ?? 'application/octet-stream' } })
  },
  websocket: {
    maxPayloadLength: 32 * 1024 * 1024,
    open(ws) {
      conns.add(ws)
      sendState(ws)
    },
    close(ws) {
      conns.delete(ws)
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
        } else if (msg.type === 'switch_soul') {
          await switchSoul(msg.name)
        }
      } catch (e) {
        console.error(e)
        ws.send(JSON.stringify({ type: 'error', error: String(e.message ?? e) }))
      }
    },
  },
})

console.log(`lydia companion on http://localhost:${PORT}`)
