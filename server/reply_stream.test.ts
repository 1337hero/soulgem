import { test, expect } from 'bun:test'
import { replyParser, type Event } from './reply_stream.ts'

/** Feed `json` in fixed-size chunks; return [events during push, events from finish]. */
function run(json: string, chunk = json.length): [Event[], Event[]] {
  const p = replyParser()
  const streamed: Event[] = []
  for (let i = 0; i < json.length; i += chunk) streamed.push(...p.push(json.slice(i, i + chunk)))
  return [streamed, p.finish()]
}

const sentences = (evs: Event[]) => evs.filter(e => e.type === 'sentence').map(e => (e as any).text)

test('full JSON in one delta', () => {
  const json = '{"emotion":"warm","reply":"The horses are ready, my Thane. We should ride."}'
  const [streamed, tail] = run(json)
  expect(streamed[0]).toEqual({ type: 'meta', meta: { emotion: 'warm' } })
  expect([...sentences(streamed), ...sentences(tail)])
    .toEqual(['The horses are ready, my Thane.', 'We should ride.'])
  expect(tail.at(-1)).toEqual({ type: 'done', thought: JSON.parse(json) })
})

test('delta split mid-escape-sequence', () => {
  const json = '{"emotion":"warm","reply":"She said \\"go\\" and left the hall."}'
  const cut = json.indexOf('\\"go') + 1  // split between the backslash and the quote
  const p = replyParser()
  p.push(json.slice(0, cut))
  p.push(json.slice(cut))
  expect((p.finish()[0] as any).text).toBe('She said "go" and left the hall.')
})

test('escaped quotes and newlines unescape', () => {
  const json = '{"emotion":"warm","reply":"He whispered \\"run\\".\\nSo we ran, hard and fast."}'
  const [streamed, tail] = run(json)
  expect([...sentences(streamed), ...sentences(tail)])
    .toEqual(['He whispered "run".', 'So we ran, hard and fast.'])
})

test('multi-sentence reply across 3-char deltas emits before finish', () => {
  const json = '{"emotion":"teasing","gesture":"none","reply":"You took your time getting here. '
    + 'The bandits nearly gave up waiting. Shall we go now, my Thane?"}'
  const [streamed, tail] = run(json, 3)
  expect(sentences(streamed).length).toBeGreaterThan(0)
  expect([...sentences(streamed), ...sentences(tail)]).toEqual([
    'You took your time getting here.',
    'The bandits nearly gave up waiting.',
    'Shall we go now, my Thane?',
  ])
})

test('short fragments merge with a neighbour', () => {
  const json = '{"emotion":"amused","reply":"Ha! Oh. That was a truly terrible idea, my Thane."}'
  const [streamed, tail] = run(json, 5)
  const all = [...sentences(streamed), ...sentences(tail)]
  expect(all).toEqual(['Ha! Oh. That was a truly terrible idea, my Thane.'])
})

test('metadata-only delta then reply field', () => {
  const meta = '{"emotion":"concerned","mood":{"MoodSad":0.3},"gesture":"idle_switch",'
  const p = replyParser()
  expect(p.push(meta)).toEqual([])
  const evs = p.push('"reply":"You are hurt, my Thane.')
  expect(evs[0]).toEqual({
    type: 'meta',
    meta: { emotion: 'concerned', mood: { MoodSad: 0.3 }, gesture: 'idle_switch' },
  })
})

test('reply with no terminal punctuation flushes at finish', () => {
  const json = '{"emotion":"neutral","reply":"I am right behind you"}'
  const [streamed, tail] = run(json, 4)
  expect(sentences(streamed)).toEqual([])
  expect(sentences(tail)).toEqual(['I am right behind you'])
})

test('done carries fields that follow the reply', () => {
  const json = '{"emotion":"proud","reply":"You did well out there today.",'
    + '"meter_delta":4,"memory_note":"The Thane spared the bandit leader."}'
  const [, tail] = run(json, 7)
  expect(tail.at(-1)).toEqual({
    type: 'done',
    thought: {
      emotion: 'proud',
      reply: 'You did well out there today.',
      meter_delta: 4,
      memory_note: 'The Thane spared the bandit leader.',
    },
  })
})

test('truncated JSON (max_tokens cut) salvages spoken sentences in done', () => {
  const p = replyParser()
  const events = p.push('{"emotion": "warm", "mood": {}, "gesture": "none", "reply": "First sentence here. Second sentence arrives now. And then it just cu')
  const done = p.finish().find(e => e.type === 'done')
  expect(done.thought.reply).toContain('First sentence here.')
  expect(done.thought.reply).toContain('Second sentence arrives now.')
  expect(done.thought.emotion).toBe('warm')
})
