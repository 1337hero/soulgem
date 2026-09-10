import { expect, test } from 'bun:test'
import { replyText } from './captions.ts'

test('every sentence appears once, whether or not the character is gesturing', () => {
  let text = replyText('Previous reply.', { seq: 0, sentence: 'First sentence.' })
  text = replyText(text, { seq: 1, sentence: 'Second sentence.' })
  text = replyText(text, { seq: 2, sentence: 'Third sentence.' })
  expect(text).toBe('First sentence. Second sentence. Third sentence.')
})
