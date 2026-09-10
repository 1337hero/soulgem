import type { Store } from './store.ts'

// Owns the lifetime of a soul's store. Jobs capture their soul before entering
// run(); retirement aborts them and closes the store only after they settle.
export class SoulWork {
  private jobs = new Map<Promise<unknown>, AbortController>()
  private retirement: Promise<void> | null = null

  constructor(private store: Pick<Store, 'close'>) {}

  run<T>(work: (controller: AbortController) => Promise<T>): Promise<T> {
    if (this.retirement) return Promise.reject(new Error('soul is retired'))
    const controller = new AbortController()
    const job = Promise.resolve().then(() => work(controller))
    this.jobs.set(job, controller)
    return job.finally(() => { this.jobs.delete(job) })
  }

  retire(): Promise<void> {
    if (!this.retirement) {
      for (const controller of this.jobs.values()) controller.abort()
      this.retirement = Promise.allSettled(this.jobs.keys()).then(() => this.store.close())
    }
    return this.retirement
  }
}

export type SoulConfig = {
  model: string
  glb?: string
  voice_ref?: string
  meter?: boolean
  tiers?: Store['tiers']
  lighting?: import('./protocol.ts').Lighting
  chat_template_kwargs?: Record<string, unknown>
}
export type Soul = {
  name: string; cfg: SoulConfig; persona: string; glbPath: string
  voiceRef: string | null; store: Store; work: SoulWork
}
export type ChatMessage = { role: 'user' | 'assistant' | 'system'; content: string }
export type ConnectionData = { history: ChatMessage[] }
export type Socket = import('bun').ServerWebSocket<ConnectionData>
