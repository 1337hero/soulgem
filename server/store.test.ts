import { test, expect } from 'bun:test'
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from 'fs'
import { tmpdir } from 'os'
import { join } from 'path'
import { Store, tierOf, classifyNote, intensityFrom, noteSimilarity, normalizeNote } from './store.ts'
import { rrfFuse, cosine, packEmbedding, unpackEmbedding, EMBED_DIMS } from './embed.ts'

const freshDir = () => mkdtempSync(join(tmpdir(), 'store-'))

/** Deterministic fake embedder from text hash — same text → same vector. */
function fakeEmbed(text: string): Float32Array {
  const v = new Float32Array(EMBED_DIMS)
  let h = 0
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) >>> 0
  for (let i = 0; i < EMBED_DIMS; i++) {
    h = (h * 1664525 + 1013904223) >>> 0
    v[i] = ((h % 1000) / 500) - 1
  }
  // L2 normalize
  let n = 0
  for (let i = 0; i < EMBED_DIMS; i++) n += v[i] * v[i]
  n = Math.sqrt(n) || 1
  for (let i = 0; i < EMBED_DIMS; i++) v[i] /= n
  return v
}

const withFake = (dir = freshDir()) =>
  new Store(dir, { embed: async (t) => fakeEmbed(t) })

test('meter clamps at 0 and 100', () => {
  const s = withFake()
  s.applyMeterDelta(-999)
  expect(s.meter).toBe(0)
  s.applyMeterDelta(999)
  expect(s.meter).toBe(100)
  s.close()
})

test('tier boundaries', () => {
  const name = (m: number) => tierOf(m)[1]
  expect([0, 19].map(name)).toEqual(['wary', 'wary'])
  expect([20, 49].map(name)).toEqual(['neutral', 'neutral'])
  expect([50, 74].map(name)).toEqual(['warm', 'warm'])
  expect([75, 100].map(name)).toEqual(['devoted', 'devoted'])
})

test('classifyNote crude heuristic', () => {
  expect(classifyNote('Three daughters. They matter.')).toBe('relationship')
  expect(classifyNote('Mike prefers direct commands.')).toBe('preference')
  expect(classifyNote('The new arrival calls himself Mike.')).toBe('identity')
  expect(classifyNote('Mike introduced me to his wife.')).toBe('relationship')
  expect(classifyNote('Mike took me to a ruin last week.')).toBe('event')
  expect(classifyNote('He thinks in connections.')).toBe('insight')
})

test('intensityFrom emotion and meter delta', () => {
  expect(intensityFrom({ emotion: 'neutral' })).toBe(1)
  expect(intensityFrom({ emotion: 'concerned' })).toBe(3)
  expect(intensityFrom({ emotion: 'warm', meterDelta: 8 })).toBe(4)
  expect(intensityFrom({ emotion: 'annoyed', meterDelta: -6 })).toBe(4)
})

test('addNote persists in sqlite and survives reload', async () => {
  const dir = freshDir()
  const s = withFake(dir)
  await s.addNote('likes sweetrolls', { emotion: 'warm' })
  await s.addNote('hates the stairs', { emotion: 'annoyed' })
  expect(existsSync(join(dir, 'user.jsonl'))).toBe(false)
  expect(s.memories.map(m => m.note)).toEqual(['likes sweetrolls', 'hates the stairs'])
  expect(s.memories[0].type).toBe('preference')
  s.close()

  const reloaded = withFake(dir)
  expect(reloaded.memories.map(m => m.note)).toEqual(['likes sweetrolls', 'hates the stairs'])
  reloaded.close()
})

test('memoryLines caps at 40', async () => {
  const s = withFake()
  for (let i = 0; i < 45; i++) await s.addNote(`unique fact number ${i} about the user`)
  const lines = s.memoryLines()
  expect(lines.length).toBe(40)
  expect(lines[0]).toContain('number 5')
  expect(lines.at(-1)).toContain('number 44')
  s.close()
})

test('state.json written on delta, read back on reload', () => {
  const dir = freshDir()
  const s = withFake(dir)
  expect(s.meter).toBe(30)
  s.applyMeterDelta(5)
  expect(JSON.parse(readFileSync(join(dir, 'state.json'), 'utf8'))).toEqual({ meter: 35 })
  s.close()
  const r = withFake(dir)
  expect(r.meter).toBe(35)
  r.close()
})

test('migrates user.jsonl then deletes it', () => {
  const dir = freshDir()
  writeFileSync(join(dir, 'user.jsonl'),
    '{"t":"2026-07-27T01:00:00.000Z","note":"drew a sword"}\n'
    + '{"t":"2026-07-27T02:00:00.000Z","note":"prefers silence"}\n')
  writeFileSync(join(dir, 'state.json'), '{"meter":33}')
  const s = withFake(dir)
  expect(s.meter).toBe(33)
  expect(s.memories.map(m => m.note)).toEqual(['drew a sword', 'prefers silence'])
  expect(s.memories[1].type).toBe('preference')
  expect(existsSync(join(dir, 'user.jsonl'))).toBe(false)
  expect(s.promptSection()).toContain('drew a sword')
  expect(s.promptSection()).toContain('Relationship: 33/100 (neutral).')
  s.close()
})

test('does not re-import if cortex.db already has notes', async () => {
  const dir = freshDir()
  const s = withFake(dir)
  await s.addNote('already here')
  s.close()
  writeFileSync(join(dir, 'user.jsonl'), '{"t":"2026-07-27T01:00:00.000Z","note":"should not import"}\n')
  const r = withFake(dir)
  expect(r.memories.map(m => m.note)).toEqual(['already here'])
  expect(existsSync(join(dir, 'user.jsonl'))).toBe(false)
  r.close()
})

test('recallSection hybrid finds keyword matches', async () => {
  const s = withFake()
  await s.addNote('Three daughters. They come up when he is not working.')
  await s.addNote('He values schedule flexibility over money.')
  await s.addNote('Mike builds AI pipelines for fun.')
  const section = await s.recallSection('how are your daughters doing')
  expect(section).toContain('Recalled for this moment')
  expect(section).toContain('daughters')
  s.close()
})

test('bulletin replaces raw lines in promptSection', async () => {
  const s = withFake()
  await s.addNote('likes sweetrolls')
  expect(s.promptSection()).toContain('likes sweetrolls')
  s.setBulletin('He is fond of sweetrolls and plain speech.')
  expect(s.promptSection()).toContain('fond of sweetrolls')
  expect(s.promptSection()).not.toContain('- likes sweetrolls')
  expect(s.isDirty()).toBe(false)
  await s.addNote('hates the stairs')
  expect(s.isDirty()).toBe(true)
  s.close()
})

test('decay fades old low-intensity notes, spares relationship and high intensity', () => {
  const dir = freshDir()
  const s = withFake(dir)
  s.db.prepare(
    `INSERT INTO notes (t, note, type, emotion, intensity, importance)
     VALUES ($t, $note, $type, '', $intensity, $importance)`,
  ).run({
    $t: '2020-01-01T00:00:00.000Z',
    $note: 'old minor insight about weather',
    $type: 'insight',
    $intensity: 1,
    $importance: 0.2,
  })
  s.db.prepare(
    `INSERT INTO notes (t, note, type, emotion, intensity, importance)
     VALUES ($t, $note, $type, '', $intensity, $importance)`,
  ).run({
    $t: '2020-01-01T00:00:00.000Z',
    $note: 'has three daughters forever',
    $type: 'relationship',
    $intensity: 2,
    $importance: 0.2,
  })
  s.db.prepare(
    `INSERT INTO notes (t, note, type, emotion, intensity, importance)
     VALUES ($t, $note, $type, '', $intensity, $importance)`,
  ).run({
    $t: '2020-01-01T00:00:00.000Z',
    $note: 'grief that defined them',
    $type: 'insight',
    $intensity: 5,
    $importance: 0.2,
  })
  s.decay()
  const byNote = Object.fromEntries(s.memories.map(m => [m.note, m]))
  expect(byNote['old minor insight about weather'].importance).toBeLessThan(0.2)
  expect(byNote['has three daughters forever'].importance).toBe(0.2)
  expect(byNote['grief that defined them'].importance).toBe(0.2)
  s.close()
})

test('per-soul tiers override the default prose', () => {
  const tiers: [number, string, string][] = [[0, 'cold', 'Ice.'], [50, 'thaw', 'Melting.']]
  const s = new Store(freshDir(), { tiers, embed: async (t) => fakeEmbed(t) })
  s.applyMeterDelta(60 - s.meter)
  expect(s.promptSection()).toContain('(thaw). Melting.')
  expect(tierOf(10, tiers)[1]).toBe('cold')
  expect(tierOf(10)[1]).toBe('wary')
  s.close()
})

test('noteSimilarity catches exact, dated, and near-dupes', () => {
  expect(normalizeNote('Mike has a secret kink. (2026-07-28)')).toBe('mike has a secret kink')
  expect(noteSimilarity(
    'Mike has a secret kink involving the aesthetics and dynamics of femboys.',
    'Mike has a secret kink involving the aesthetics and dynamics of femboys.',
  )).toBe(1)
  expect(noteSimilarity(
    'Mike has a secret kink.',
    'Mike has a secret kink involving the aesthetics and dynamics of femboys.',
  )).toBeGreaterThan(0.72)
  expect(noteSimilarity(
    'Mike is actively trying to decompress and lower his stress levels tonight.',
    'Mike is actively trying to decompress and lower his stress levels tonight. (2026-07-28)',
  )).toBe(1)
  expect(noteSimilarity(
    'Three daughters. They matter.',
    'Mike builds AI pipelines.',
  )).toBeLessThan(0.3)
})

test('consolidate suppresses near-duplicates, keeps the richer note', async () => {
  const s = withFake()
  await s.addNote('Mike has a secret kink.')
  await s.addNote('Mike has a secret kink involving the aesthetics and dynamics of femboys.')
  await s.addNote('Mike has a secret kink involving the aesthetics and dynamics of femboys.')
  await s.addNote('Three daughters. They matter.')
  expect(s.consolidate()).toBe(2)
  expect(s.consolidate()).toBe(0)
  const notes = s.memories.map(m => m.note)
  expect(notes).toContain('Mike has a secret kink involving the aesthetics and dynamics of femboys.')
  expect(notes).toContain('Three daughters. They matter.')
  expect(notes).not.toContain('Mike has a secret kink.')
  expect(notes.length).toBe(2)
  expect(s.isDirty()).toBe(true)
  s.close()
})

test('rrfFuse merges ranked lists', () => {
  expect(rrfFuse([[1, 2, 3], [2, 4, 1]]).slice(0, 3)).toEqual([2, 1, 4])
})

test('pack/unpack embedding roundtrip', () => {
  const v = fakeEmbed('hello')
  const buf = packEmbedding(v)
  const back = unpackEmbedding(buf)!
  expect(back.length).toBe(EMBED_DIMS)
  expect(cosine(v, back)).toBeCloseTo(1, 5)
})

test('addNote stores embedding; backfill fills missing', async () => {
  const s = withFake()
  await s.addNote('kids and family matter a lot')
  const row = s.db.query('SELECT embedding FROM notes WHERE id = 1').get() as { embedding: Uint8Array }
  expect(row.embedding).toBeTruthy()
  expect(unpackEmbedding(row.embedding)!.length).toBe(EMBED_DIMS)

  // simulate migrated note without embedding
  s.db.prepare(
    `INSERT INTO notes (t, note, type, emotion, intensity, importance)
     VALUES ('2020-01-01T00:00:00.000Z', 'schedule freedom over salary', 'preference', '', 2, 0.4)`,
  ).run()
  expect(await s.backfillEmbeddings()).toBe(1)
  expect(await s.backfillEmbeddings()).toBe(0)
  s.close()
})
