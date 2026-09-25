import { expect, test } from 'bun:test'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createCompanion } from './companion.js'
import { Store } from './store.ts'
import type { ServerMsg } from './protocol.ts'

function souls() {
  const root = mkdtempSync(join(tmpdir(), 'companion-test-'))
  for (const name of ['old', 'new']) {
    const dir = join(root, 'souls', name)
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, 'persona.md'), `You are ${name}.`)
    writeFileSync(join(dir, 'config.json'), JSON.stringify({ model: name }))
  }
  return root
}

async function connect(port: number) {
  const socket = new WebSocket(`ws://127.0.0.1:${port}/ws`)
  const messages: ServerMsg[] = []
  socket.onmessage = event => messages.push(JSON.parse(String(event.data)))
  await new Promise<void>((resolve, reject) => { socket.onopen = () => resolve(); socket.onerror = reject })
  return { socket, messages }
}

async function until(predicate: () => boolean) {
  for (let attempt = 0; attempt < 200; attempt++) {
    if (predicate()) return
    await Bun.sleep(5)
  }
  throw new Error('timed out waiting for companion event')
}

test('switching souls cancels an in-flight transcription before it becomes a turn', async () => {
  const root = souls()
  const started = Promise.withResolvers<void>()
  const transcript = Promise.withResolvers<string>()
  const llm = Bun.serve({ port: 0, fetch: () => Response.json({ choices: [] }) })
  const app = await createCompanion({ root, port: 0, bootSoul: 'old',
    llmUrl: llm.url.href, embed: async () => null,
    stages: { transcribe: async () => { started.resolve(); return transcript.promise },
      synthesize: async () => Buffer.alloc(0), lipSync: async () => [] },
  })
  const { socket, messages } = await connect(app.server.port!)
  try {
    socket.send(JSON.stringify({ type: 'audio', data: '' }))
    await started.promise
    socket.send(JSON.stringify({ type: 'switch_soul', name: 'new' }))
    await until(() => messages.some(m => m.type === 'soul'))
    transcript.resolve('A message for the old soul.')
    await app.stop()
    expect(messages.filter(m => m.type === 'transcript' || m.type === 'speak' || m.type === 'error')).toEqual([])
  } finally {
    transcript.resolve('finished')
    socket.close()
    await app.stop()
    await llm.stop(true)
    rmSync(root, { recursive: true, force: true })
  }
})

test('switching during bulletin generation never writes the old briefing into the new soul', async () => {
  const root = souls()
  const store = new Store(join(root, 'souls/old/memory'), { embed: async () => null })
  await store.addNote('Mike likes hiking.')
  store.close()
  const started = Promise.withResolvers<void>()
  const briefing = Promise.withResolvers<Response>()
  const llm = Bun.serve({ port: 0, async fetch(req) {
    const body = await req.json()
    if (body.max_tokens === 500) { started.resolve(); return briefing.promise }
    return Response.json({ choices: [] })
  } })
  const app = await createCompanion({ root, port: 0, bootSoul: 'old',
    llmUrl: llm.url.href, idleMs: 10, embed: async () => null,
  })
  const { socket, messages } = await connect(app.server.port!)
  try {
    await started.promise
    socket.send(JSON.stringify({ type: 'switch_soul', name: 'new' }))
    await until(() => messages.some(m => m.type === 'soul'))
    briefing.resolve(Response.json({ choices: [{ message: { content: 'Old private briefing' } }] }))
    await app.stop()
    const fresh = new Store(join(root, 'souls/new/memory'), { embed: async () => null })
    expect(fresh.getBulletin()).toBeNull()
    expect(fresh.memories).toEqual([])
    fresh.close()
    expect(messages.filter(m => m.type === 'error')).toEqual([])
  } finally {
    briefing.resolve(Response.json({ choices: [] }))
    socket.close()
    await app.stop()
    await llm.stop(true)
    rmSync(root, { recursive: true, force: true })
  }
})

test('a text turn streams ordered audio, typed metadata and an end message', async () => {
  const root = souls()
  const reply = { emotion: 'warm', mood: {}, gesture: 'none',
    reply: 'First sentence is ready. Second sentence follows.', meter_delta: 0, memory_note: null }
  const llm = Bun.serve({ port: 0, async fetch(req) {
    const body = await req.json()
    if (!body.stream) return Response.json({ choices: [] })
    return new Response(`data: ${JSON.stringify({ choices: [{ delta: { content: JSON.stringify(reply) } }] })}\n\ndata: [DONE]\n\n`)
  } })
  const app = await createCompanion({ root, port: 0, bootSoul: 'old', llmUrl: llm.url.href,
    embed: async () => null, stages: { transcribe: async () => '',
      synthesize: async text => Buffer.from(text), lipSync: async () => [] },
  })
  const { socket, messages } = await connect(app.server.port!)
  try {
    socket.send(JSON.stringify({ type: 'text', text: 'Hello there' }))
    await until(() => messages.some(m => m.type === 'speak_end' || m.type === 'error'))
    const chunks = messages.filter(m => m.type === 'speak')
    expect(chunks.map(m => m.seq)).toEqual([0, 1])
    expect(chunks.map(m => m.sentence)).toEqual(['First sentence is ready.', 'Second sentence follows.'])
    expect(chunks[0].tier).toBe('neutral')
    expect(messages.some(m => m.type === 'speak_end')).toBe(true)
    expect(messages.filter(m => m.type === 'error')).toEqual([])
  } finally {
    socket.close()
    await app.stop()
    await llm.stop(true)
    rmSync(root, { recursive: true, force: true })
  }
})

test('a later synthesis failure is handled immediately and reported after pending audio settles', async () => {
  const root = souls()
  const first = Promise.withResolvers<Buffer<ArrayBuffer>>()
  const second = Promise.withResolvers<void>()
  const llm = Bun.serve({ port: 0, async fetch(req) {
    if (!(await req.json()).stream) return Response.json({ choices: [] })
    const reply = { emotion: 'warm', reply: 'First sentence is ready. Second sentence will fail.' }
    return new Response(`data: ${JSON.stringify({ choices: [{ delta: { content: JSON.stringify(reply) } }] })}\n\n`)
  } })
  let count = 0
  const app = await createCompanion({ root, port: 0, bootSoul: 'old', llmUrl: llm.url.href,
    embed: async () => null, stages: { transcribe: async () => '', lipSync: async () => [],
      synthesize: async () => {
        if (++count === 1) return first.promise
        second.resolve()
        throw new Error('synthesis unavailable')
      } },
  })
  const { socket, messages } = await connect(app.server.port!)
  try {
    socket.send(JSON.stringify({ type: 'text', text: 'Hello' }))
    await second.promise
    await Bun.sleep(0)
    first.resolve(Buffer.from('audio'))
    await until(() => messages.some(m => m.type === 'error'))
    expect(messages.filter(m => m.type === 'error')).toEqual([{ type: 'error', error: 'synthesis unavailable' }])
    expect(messages.some(m => m.type === 'speak_end')).toBe(false)
  } finally {
    first.resolve(Buffer.from('audio'))
    socket.close()
    await app.stop()
    await llm.stop(true)
    rmSync(root, { recursive: true, force: true })
  }
})

test('static files revalidate by ETag and text assets arrive gzipped', async () => {
  const root = souls()
  mkdirSync(join(root, 'anims'))
  writeFileSync(join(root, 'anims/index.json'), JSON.stringify(Array(200).fill('mt_idle')))
  const llm = Bun.serve({ port: 0, fetch: () => Response.json({ choices: [] }) })
  const app = await createCompanion({ root, port: 0, bootSoul: 'old', llmUrl: llm.url.href, embed: async () => null })
  const url = `http://127.0.0.1:${app.server.port}/anims/index.json`
  try {
    const first = await fetch(url, { headers: { 'Accept-Encoding': 'gzip' }, decompress: false })
    expect(first.headers.get('Content-Encoding')).toBe('gzip')
    expect(first.headers.get('Cache-Control')).toBe('no-cache')
    expect(JSON.parse(new TextDecoder().decode(Bun.gunzipSync(await first.bytes())))).toHaveLength(200)

    const etag = first.headers.get('ETag') ?? ''
    const again = await fetch(url, { headers: { 'If-None-Match': etag } })
    expect(again.status).toBe(304)

    writeFileSync(join(root, 'anims/index.json'), '[]')
    const changed = await fetch(url, { headers: { 'If-None-Match': etag } })
    expect(changed.status).toBe(200)
    expect(await changed.json()).toEqual([])
  } finally {
    await app.stop()
    await llm.stop(true)
    rmSync(root, { recursive: true, force: true })
  }
})
