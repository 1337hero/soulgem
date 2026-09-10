import { expect, test } from 'bun:test'
import { createPlayback, type PreparedAudio } from './playback.ts'
import type { ServerMsg } from '../server/protocol.ts'

type Chunk = Extract<ServerMsg, { type: 'speak' }>
const chunk = (seq: number): Chunk => ({
  type: 'speak', seq, sentence: `Sentence ${seq}.`, mood: {}, gesture: 'none',
  meter: 30, tier: 'neutral', visemes: null, audio: '',
})

test('interruption during decode prevents canceled audio from starting', async () => {
  const decoding = Promise.withResolvers<void>()
  const decoded = Promise.withResolvers<PreparedAudio>()
  let starts = 0
  const playback = createPlayback({
    prepare: async () => { decoding.resolve(); return decoded.promise },
    onChunk: () => {}, onIdle: () => {}, onStop: () => {}, onError: () => {},
  })
  const pending = playback.enqueue(chunk(0))
  await decoding.promise
  playback.stop()
  decoded.resolve(() => { starts++; return { stop() {}, startedAt: 0 } })
  await pending
  expect(starts).toBe(0)
  expect(playback.current).toBeNull()
  expect(playback.speaking).toBe(false)
})

test('sentences stay ordered across starvation and finish only after the last audio', async () => {
  const ends: (() => void)[] = []
  const spoken: string[] = []
  let finished = 0
  const playback = createPlayback({
    prepare: async (_, ended) => {
      ends.push(ended)
      return () => ({ stop() {}, startedAt: 1 })
    },
    onChunk: msg => spoken.push(msg.sentence), onIdle: () => finished++,
    onStop: () => {}, onError: error => { throw error },
  })
  await playback.enqueue(chunk(0))
  ends[0]()
  expect(playback.speaking).toBe(false)
  expect(finished).toBe(0)
  await playback.enqueue(chunk(1))
  playback.end()
  expect(finished).toBe(0)
  ends[1]()
  expect(spoken).toEqual(['Sentence 0.', 'Sentence 1.'])
  expect(finished).toBe(1)
})

test('a stale decode failure cannot stop a newer reply', async () => {
  const stale = Promise.withResolvers<PreparedAudio>()
  const errors: unknown[] = []
  let requests = 0
  const playback = createPlayback({
    prepare: async () => ++requests === 1 ? stale.promise : () => ({ stop() {}, startedAt: 2 }),
    onChunk: () => {}, onIdle: () => {}, onStop: () => {}, onError: error => errors.push(error),
  })
  const pending = playback.enqueue(chunk(0))
  playback.stop()
  await playback.enqueue(chunk(0))
  stale.reject(new Error('old decode failed'))
  await pending
  expect(playback.speaking).toBe(true)
  expect(errors).toEqual([])
})
