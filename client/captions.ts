import type { SpeechChunk } from './playback.ts'

export function replyText(previous: string, chunk: Pick<SpeechChunk, 'seq' | 'sentence'>) {
  return chunk.seq === 0 ? chunk.sentence : `${previous} ${chunk.sentence}`.trim()
}
