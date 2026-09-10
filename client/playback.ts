import type { ServerMsg } from '../server/protocol.ts'

export type SpeechChunk = Extract<ServerMsg, { type: 'speak' }>
type PlayingAudio = { stop(): void; startedAt: number }
export type PreparedAudio = () => PlayingAudio

type Options = {
  prepare(chunk: SpeechChunk, ended: () => void): Promise<PreparedAudio>
  onChunk(chunk: SpeechChunk): void
  onIdle(): void
  onStop(): void
  onError(error: unknown): void
}

// A generation belongs to one reply. Stopping invalidates pending decodes as
// well as playing audio; an old promise must never restart a canceled reply.
export function createPlayback(options: Options) {
  const queue: SpeechChunk[] = []
  let generation = 0
  let current: SpeechChunk | null = null
  let active: PlayingAudio | null = null
  let ended = false

  function finish() {
    if (ended && !current && !queue.length) {
      ended = false
      options.onIdle()
    }
  }

  function stop() {
    generation++
    active?.stop()
    active = null
    current = null
    queue.length = 0
    ended = false
    options.onStop()
  }

  async function playNext() {
    if (current) return
    const chunk = queue.shift()
    if (!chunk) { finish(); return }
    const turn = generation
    current = chunk
    try {
      const start = await options.prepare(chunk, () => {
        if (turn !== generation || current !== chunk) return
        active = null
        current = null
        void playNext()
      })
      if (turn !== generation) return
      options.onChunk(chunk)
      active = start()
    } catch (error) {
      if (turn !== generation) return
      stop()
      options.onError(error)
    }
  }

  return {
    enqueue(chunk: SpeechChunk) { queue.push(chunk); return playNext() },
    end() { ended = true; finish() },
    stop,
    patchVisemes(seq: number, visemes: SpeechChunk['visemes']) {
      const chunk = current?.seq === seq ? current : queue.find(item => item.seq === seq)
      if (chunk) chunk.visemes = visemes
    },
    get current() { return current },
    get speaking() { return active !== null },
    get startedAt() { return active?.startedAt ?? 0 },
  }
}
