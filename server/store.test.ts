import { test, expect } from 'bun:test'
import { mkdtempSync, readFileSync, writeFileSync } from 'fs'
import { tmpdir } from 'os'
import { join } from 'path'
import { Store, tierOf } from './store.ts'

const freshDir = () => mkdtempSync(join(tmpdir(), 'store-'))

test('meter clamps at 0 and 100', () => {
  const s = new Store(freshDir())
  s.applyMeterDelta(-999)
  expect(s.meter).toBe(0)
  s.applyMeterDelta(999)
  expect(s.meter).toBe(100)
})

test('tier boundaries', () => {
  const name = (m: number) => tierOf(m)[1]
  expect([0, 19].map(name)).toEqual(['wary', 'wary'])
  expect([20, 49].map(name)).toEqual(['neutral', 'neutral'])
  expect([50, 74].map(name)).toEqual(['warm', 'warm'])
  expect([75, 100].map(name)).toEqual(['devoted', 'devoted'])
})

test('addNote appends one line and survives reload', () => {
  const dir = freshDir()
  const s = new Store(dir)
  s.addNote('likes sweetrolls')
  s.addNote('hates the stairs')
  const lines = readFileSync(join(dir, 'user.jsonl'), 'utf8').split('\n').filter(Boolean)
  expect(lines.length).toBe(2)
  expect(JSON.parse(lines[0]).note).toBe('likes sweetrolls')

  const reloaded = new Store(dir)
  expect(reloaded.memories.map(m => m.note)).toEqual(['likes sweetrolls', 'hates the stairs'])
})

test('memoryLines caps at 40', () => {
  const s = new Store(freshDir())
  for (let i = 0; i < 45; i++) s.addNote(`note ${i}`)
  const lines = s.memoryLines()
  expect(lines.length).toBe(40)
  expect(lines[0]).toContain('note 5')
  expect(lines.at(-1)).toContain('note 44')
})

test('state.json written on delta, read back on reload', () => {
  const dir = freshDir()
  const s = new Store(dir)
  expect(s.meter).toBe(30)
  s.applyMeterDelta(5)
  expect(JSON.parse(readFileSync(join(dir, 'state.json'), 'utf8'))).toEqual({ meter: 35 })
  expect(new Store(dir).meter).toBe(35)
})

test('loads existing files in the live format', () => {
  const dir = freshDir()
  writeFileSync(join(dir, 'user.jsonl'), '{"t":"2026-07-27T01:00:00.000Z","note":"drew a sword"}\n')
  writeFileSync(join(dir, 'state.json'), '{"meter":33}')
  const s = new Store(dir)
  expect(s.meter).toBe(33)
  expect(s.promptSection()).toContain('- drew a sword (2026-07-27)')
  expect(s.promptSection()).toContain('Relationship: 33/100 (neutral).')
})

test('per-soul tiers override the default prose', () => {
  const tiers: [number, string, string][] = [[0, 'cold', 'Ice.'], [50, 'thaw', 'Melting.']]
  const s = new Store(freshDir(), { tiers })
  s.applyMeterDelta(60 - s.meter)
  expect(s.promptSection()).toContain('(thaw). Melting.')
  expect(tierOf(10, tiers)[1]).toBe('cold')
  expect(tierOf(10)[1]).toBe('wary')  // default table untouched
})
