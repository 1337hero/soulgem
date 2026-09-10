// Lydia companion orchestrator: static viewer + WS voice loop.
//   bun run server/companion.js
// Pipeline per turn: audio -> whisper-server -> GLM (persona, JSON) -> TTS -> client.
import { existsSync, readdirSync, readFileSync } from 'fs'
import { dirname, join } from 'path'
import { REPLY_SCHEMA, EMOTION_MOOD, outputFormatDoc, sendServer, parseClientMsg } from './protocol.ts'
import { replyParser } from './reply_stream.ts'
import * as defaultStages from './stages.js'
import { SoulWork } from './soul.ts'
import { embedText } from './embed.ts'
import { Store, tierOf } from './store.ts'

export async function createCompanion({
  root = dirname(import.meta.dir), port = 8471, bootSoul = Bun.env.SOUL ?? 'lydia',
  llmUrl = 'http://127.0.0.1:8082/v1/chat/completions', stages = defaultStages,
  idleMs = 60_000, embed = embedText,
} = {}) {
  const ROOT = root
  const PORT = port
  const LLM_URL = llmUrl
  const { transcribe, synthesize, lipSync } = stages

  // ---- soul pack: persona + config + memory namespace (SOUL env selects the
  // boot soul; everything soul-scoped hangs off `soul` so a switch is one swap) ----
  /** @param {string} name */
  async function loadSoul(name) {
    const dir = join(ROOT, 'souls', name)
    if (!(await Bun.file(join(dir, 'persona.md')).exists())) {
      throw new Error(`no such soul: souls/${name}/`)
    }
    /** @type {import('./soul.ts').SoulConfig} */
    const cfg = await Bun.file(join(dir, 'config.json')).json()
    const store = new Store(join(dir, 'memory'), { meter: cfg.meter !== false, tiers: cfg.tiers, embed })
    const s = {
      name,
      cfg,
      persona: await Bun.file(join(dir, 'persona.md')).text(),
      glbPath: join(ROOT, cfg.glb ?? 'characters/lydia/lydia.glb'),
      // absolute path for the per-request TTS clone ref; null = server default
      voiceRef: cfg.voice_ref
        ? (cfg.voice_ref.startsWith('~') ? cfg.voice_ref.replace('~', Bun.env.HOME ?? '')
                                         : join(ROOT, cfg.voice_ref))
        : null,
      store, work: new SoulWork(store),
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

  let soul = await loadSoul(bootSoul)

  // every soul with both files is switchable; `active` marks the live one
  function listSouls() {
    return readdirSync(join(ROOT, 'souls'), { withFileTypes: true })
      .filter(d => d.isDirectory()
        && existsSync(join(ROOT, 'souls', d.name, 'persona.md'))
        && existsSync(join(ROOT, 'souls', d.name, 'config.json')))
      .map(d => {
        const cfg = JSON.parse(readFileSync(join(ROOT, 'souls', d.name, 'config.json'), 'utf8'))
        return { name: d.name, model: cfg.model, glb: cfg.glb ?? 'characters/lydia/lydia.glb', active: d.name === soul.name }
      })
  }

  /** @type {Set<import('./soul.ts').Socket>} */
  const conns = new Set()  // live WS connections, for switch broadcasts

  /** @param {import('./soul.ts').Socket} ws */
  function sendState(ws) {
    // meter otherwise only rides the seq-0 speak chunk, so a fresh page shows
    // nothing until she talks. Null when the soul runs meterless.
    const on = soul.cfg.meter !== false
    sendServer(ws, {
      type: 'state',
      meter: on ? soul.store.meter : null,
      tier: on ? tierOf(soul.store.meter, soul.store.tiers)[1] : null,
      lighting: soul.cfg.lighting ?? null,
    })
  }

  // Load first so a bad name changes nothing; then cancel in-flight turns
  // (same path as barge-in), reset every connection's context, swap, announce.
  let switching = Promise.resolve()
  /** @param {string} name */
  function switchSoul(name) {
    const change = switching.then(() => performSwitch(name))
    switching = change.catch(() => {})
    return change
  }

  /** @param {string} name */
  async function performSwitch(name) {
    if (name === soul.name) return
    const next = await loadSoul(name)
    for (const c of conns) {
      const token = activeTurn.get(c)
      if (token) token.abort()
      c.data.history = []  // new persona = new conversation
    }
    const prev = soul
    soul = next
    const retired = prev.work.retire()
    console.log(`soul -> ${name}`)
    for (const c of conns) {
      sendState(c)
      sendServer(c, { type: 'soul', name: soul.name, glb: `/body.glb?v=${soul.name}` })
    }
    await retired
    armIdle()
  }

  // Always-on: persona + schema + bulletin/meter. Per-turn: hybrid FTS+MiniLM recall.
  /** @param {import('./soul.ts').Soul} soul
   * @param {string} userText */
  async function systemPrompt(soul, userText) {
    const recall = userText ? await soul.store.recallSection(userText) : ''
    return soul.persona + '\n' + outputFormatDoc() + soul.store.promptSection() + recall
  }

  /** @param {import('./soul.ts').Soul} soul
   * @param {import('./protocol.ts').Reply} parsed */
  async function applyThought(soul, parsed) {
    if (typeof parsed.meter_delta === 'number') soul.store.applyMeterDelta(parsed.meter_delta)
    if (parsed.memory_note) {
      await soul.store.addNote(parsed.memory_note, {
        emotion: parsed.emotion,
        meterDelta: parsed.meter_delta,
      })
    }
  }

  // ---- cortex-lite: decay + bulletin regen after 60s session idle ----
  const IDLE_MS = idleMs
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  let idleTimer
  let maintaining = false
  let stopped = false

  function armIdle() {
    clearTimeout(idleTimer)
    if (stopped || [...conns].some(c => activeTurn.has(c))) return
    idleTimer = setTimeout(() => {
      maintainCortex().catch(e => console.error('cortex maintain:', e?.message ?? e))
    }, IDLE_MS)
  }



  async function maintainCortex() {
    if (maintaining) return
    maintaining = true
    const captured = soul
    try {
      await captured.work.run(async token => {
        try { await maintainSoul(captured, token) }
        catch (error) { if (!token.signal.aborted) throw error }
      })
    } finally {
      maintaining = false
    }
  }

  /** @param {import('./soul.ts').Soul} soul
   * @param {AbortController} token */
  async function maintainSoul(soul, token) {
    if (token.signal.aborted) return
    const faded = soul.store.decay()
    if (faded) console.log(`cortex decay: ${faded} note(s) faded`)
    const dupes = soul.store.consolidate()
    if (dupes) console.log(`cortex consolidate: ${dupes} duplicate(s) suppressed`)
    const embedded = await soul.store.backfillEmbeddings()
    if (embedded) console.log(`cortex embed: ${embedded} note(s) vectorized`)
    if (token.signal.aborted) return
    if (!soul.store.isDirty() && soul.store.getBulletin()) return
    const notes = soul.store.notesForBulletin()
    if (!notes.length) return
    const text = await synthesizeBulletin(soul, notes, token.signal)
    if (text && !token.signal.aborted) {
      soul.store.setBulletin(text)
      console.log(`cortex bulletin: ${text.split(/\s+/).length} words`)
    }
  }

  /** @param {import('./soul.ts').Soul} soul
   * @param {import('./store.ts').Note[]} notes
   * @param {AbortSignal} signal */
  async function synthesizeBulletin(soul, notes, signal) {
    const body = notes.map(n =>
      `- [${n.type}|intensity:${n.intensity}] ${n.note}`).join('\n')
    const res = await fetch(LLM_URL, {
      method: 'POST', signal,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        model: soul.cfg.model,
        messages: [
          {
            role: 'system',
            content: `You synthesize working memory for a companion character.
Write a concise briefing (under 300 words) about the person they talk with.
Second person where natural ("They…", "You know that…"). Prioritize high-intensity
and relationship/identity facts. Coherent prose, not a bullet dump. No markdown
fences, no IDs, no meta commentary — only the briefing.`,
          },
          { role: 'user', content: `Notes:\n${body}\n\nWrite the working-memory briefing.` },
        ],
        temperature: 0.4,
        max_tokens: 500,
        chat_template_kwargs: soul.cfg.chat_template_kwargs ?? {},
      }),
    })
    if (!res.ok) {
      console.log(`cortex bulletin llm failed: ${res.status}`)
      return null
    }
    const data = await res.json()
    const text = data.choices?.[0]?.message?.content?.trim()
    return text || null
  }

  // Per-connection conversation history. HISTORY_CAP is what we KEEP;
  // thinkStream sends only the last 24 messages (12 exchanges) to the LLM —
  // the extra retention is slack, not prompt context.
  const HISTORY_CAP = 48  // ponytail: hard cap; nothing to configure yet
  /** @param {import('./soul.ts').ChatMessage[]} history
   * @param {import('./soul.ts').ChatMessage} msg */
  function push(history, msg) {
    history.push(msg)
    if (history.length > HISTORY_CAP) history.splice(0, history.length - HISTORY_CAP)
  }

  // Streams the LLM response, yielding events as they become available:
  //   {type:'meta', meta}      — emotion/mood/gesture/... (before reply text starts)
  //   {type:'sentence', text}  — each completed sentence of the reply
  //   {type:'done', thought}   — full parsed object at the end
  /** @param {import('./soul.ts').Soul} soul
   * @param {string} userText
   * @param {AbortController} token
   * @param {import('./soul.ts').ChatMessage[]} history */
  async function* thinkStream(soul, userText, token, history) {
    // pushed before the stream; a barge-in mid-turn leaves this user message
    // unanswered in history. Intentional — the model tolerates it, and the
    // interrupted question is real context for the next turn.
    push(history, { role: 'user', content: userText })
    const res = await fetch(LLM_URL, {
      method: 'POST', signal: token.signal,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        model: soul.cfg.model,
        messages: [{ role: 'system', content: await systemPrompt(soul, userText) }, ...history.slice(-24)],
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
    if (!res.body) throw new Error('llm response has no body')
    const reader = res.body.getReader()
    const dec = new TextDecoder()
    let sse = ''
    try {
      while (true) {
        if (token.signal.aborted) return
        const { done, value } = await reader.read()
        if (done) break
        sse += dec.decode(value, { stream: true })
        const lines = sse.split('\n')
        sse = lines.pop() ?? ''
        for (const line of lines) {
          if (!line.startsWith('data: ') || line === 'data: [DONE]') continue
          const delta = JSON.parse(line.slice(6)).choices?.[0]?.delta?.content
          if (delta) yield* parser.push(delta)
        }
      }
    } finally {
      reader.cancel().catch(() => {})
    }
    if (token.signal.aborted) return

    for (const ev of parser.finish()) {
      if (ev.type === 'done') {
        push(history, { role: 'assistant', content: ev.thought.reply })
        await applyThought(soul, ev.thought)
      }
      yield ev
    }
  }

  /** @type {WeakMap<import('./soul.ts').Socket, AbortController>} */
  const activeTurn = new WeakMap()  // ws -> cancellation token for the in-flight turn

  /** @param {import('./soul.ts').Socket} ws
   * @param {Extract<import('./protocol.ts').ClientMsg, {type: 'text' | 'audio'}>} input */
  async function startTurn(ws, input) {
    const captured = soul
    return captured.work.run(async token => {
      activeTurn.get(ws)?.abort()
      activeTurn.set(ws, token)
      clearTimeout(idleTimer)
      try {
        const text = input.type === 'audio'
          ? await transcribe(Buffer.from(input.data, 'base64')) : input.text
        if (token.signal.aborted) return
        if (!text || (input.type === 'audio' && text.length < 2)) throw new Error('heard nothing')
        await handleTurn(captured, ws, text, token)
      } catch (error) {
        if (!token.signal.aborted || token.signal.reason === error) throw error
      } finally {
        if (activeTurn.get(ws) === token) activeTurn.delete(ws)
        armIdle()
      }
    })
  }

  /** @param {import('./soul.ts').Soul} soul
   * @param {import('./soul.ts').Socket} ws
   * @param {string} userText
   * @param {AbortController} token */
  async function handleTurn(soul, ws, userText, token) {
    const t0 = Date.now()
    sendServer(ws, { type: 'transcript', text: userText })
  /** @type {import('./protocol.ts').ReplyMeta | null} */
    let meta = null
    let seq = 0
    let tFirst = 0
    /** @type {unknown} */
    let stageError = null          // first synth/lipsync failure; rethrown after the chain drains
    let interrupted = false        // log barge-in once, not per pending chunk
    let sendChain = Promise.resolve()  // serializes speak messages in seq order

    try {
      for await (const ev of thinkStream(soul, userText, token, ws.data.history)) {
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
            if (token.signal.aborted) return null
            const wav = await synthesize(ev.text, soul.voiceRef)
            return token.signal.aborted ? null : { wav }
          })().catch(error => {
            stageError ??= error
            token.abort(error)
            return null
          })
          sendChain = sendChain.then(async () => {
            const ready = await job
            if (!ready || token.signal.aborted) {
              if (!interrupted) console.log(`turn interrupted at chunk ${mySeq} — "${ev.text.slice(0, 40)}"`)
              interrupted = true
              return
            }
            const mood = meta?.mood && Object.keys(meta.mood).length ? meta.mood
                       : EMOTION_MOOD[meta?.emotion ?? 'neutral'] ?? {}
            tFirst ||= Date.now()
            sendServer(ws, {
              type: 'speak', seq: mySeq, sentence: ev.text,
              emotion: meta?.emotion, mood, gesture: mySeq === 0 ? meta?.gesture ?? 'none' : 'none',
              meter: soul.cfg.meter === false ? null : soul.store.meter,
              tier: soul.cfg.meter === false ? null : tierOf(soul.store.meter, soul.store.tiers)[1],
              visemes: null, audio: ready.wav.toString('base64'),
            })
            lipSync(ready.wav, ev.text).then(visemes => {
              if (visemes && !token.signal.aborted) {
                sendServer(ws, { type: 'visemes', seq: mySeq, visemes })
              }
            }).catch(() => {})  // no track = the chunk stays jaw-flapped
          })
        } else if (ev.type === 'done') {
          await sendChain
          if (stageError) throw stageError
          if (token.signal.aborted) return
          sendServer(ws, { type: 'speak_end' })
          console.log(`turn: first audio ${tFirst - t0}ms, total ${Date.now() - t0}ms, ${seq} chunk(s) — "${ev.thought.reply.slice(0, 60)}"`)
        }
      }
      await sendChain  // cancelled mid-stream: let queued (skipping) links settle
      if (stageError) throw stageError
    } finally {
      await sendChain
      if (stageError) throw stageError
    }
  }

  /** @type {Record<string, string>} */
  const MIME = { html: 'text/html', js: 'text/javascript', json: 'application/json',
                 glb: 'model/gltf-binary', png: 'image/png', css: 'text/css',
                 ogg: 'audio/ogg' }

  const server = Bun.serve({
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
      const ext = path.split('.').pop() ?? ''
      return new Response(file, { headers: { 'Content-Type': MIME[ext] ?? 'application/octet-stream' } })
    },
    websocket: {
      data: /** @type {import('./soul.ts').ConnectionData} */ ({ history: [] }),
      maxPayloadLength: 32 * 1024 * 1024,
      open(ws) {
        conns.add(ws)
        sendState(ws)
      },
      close(ws) {
        activeTurn.get(ws)?.abort()
        conns.delete(ws)
      },
      async message(ws, raw) {
        try {
          const msg = parseClientMsg(JSON.parse(String(raw)))
          if (msg.type === 'audio' || msg.type === 'text') {
            await startTurn(ws, msg)
          } else if (msg.type === 'interrupt') {
            const token = activeTurn.get(ws)
            if (token) token.abort()
          } else if (msg.type === 'switch_soul') {
            await switchSoul(msg.name)
          }
        } catch (e) {
          console.error(e)
          sendServer(ws, { type: 'error', error: String(e instanceof Error ? e.message : e) })
        }
      },
    },
  })

  armIdle()
  return {
    server,
    async stop() {
      stopped = true
      clearTimeout(idleTimer)
      for (const ws of conns) ws.close()
      await switching
      await soul.work.retire()
      await server.stop(true)
    },
  }
}

if (import.meta.main) {
  const app = await createCompanion()
  console.log(`lydia companion on http://localhost:${app.server.port}`)
}
