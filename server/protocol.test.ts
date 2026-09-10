import { expect, test } from 'bun:test'
import { parseClientMsg, parseServerMsg, sendServer, type ServerMsg } from './protocol.ts'

test('speech messages round-trip with tier, meterless state and late visemes', () => {
  const messages: ServerMsg[] = [
    { type: 'speak', seq: 0, sentence: 'Hello there.', emotion: 'warm', mood: {}, gesture: 'none',
      meter: null, tier: null, visemes: null, audio: '' },
    { type: 'visemes', seq: 0, visemes: [{ s: 0, e: 1, v: 'BigAah' }] },
    { type: 'state', meter: 30, tier: 'neutral', lighting: { key: { color: '#fff', intensity: 2 } } },
  ]
  for (const message of messages) {
    sendServer({ send(raw) { expect(parseServerMsg(JSON.parse(raw))).toEqual(message) } }, message)
  }
})

test('malformed wire messages fail at the boundary', () => {
  expect(() => parseClientMsg({ type: 'text', text: 42 })).toThrow()
  expect(() => parseClientMsg({ type: 'unknown' })).toThrow()
  expect(() => parseServerMsg({ type: 'visemes', seq: 0, visemes: [{ s: 'zero', e: 1, v: null }] })).toThrow()
})
