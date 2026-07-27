// Durable single-user state: memory notes + relationship meter.
// Files stay exactly as before: memory/user.jsonl (one {t, note} per line), memory/state.json ({"meter": n}).
import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'fs'
import { join } from 'path'

export type Note = { t: string; note: string }

export const METER_TIERS: [number, string, string][] = [
  [0,  'wary',    'You barely trust this Thane yet. Formal, clipped, strictly professional.'],
  [20, 'neutral', 'Professional respect. Dry, dutiful, keeps a little distance.'],
  [50, 'warm',    'Years of earned trust. Relaxed, teases freely, quietly fond.'],
  [75, 'devoted', 'Deep loyalty, chosen not sworn. Openly fond beneath the deadpan; protective.'],
]
export const tierOf = (m: number) => METER_TIERS.findLast(([min]) => m >= min)!

const MEMORY_CAP = 40

export class Store {
  memFile: string
  stateFile: string
  memories: Note[]
  meter: number
  meterOn: boolean

  // ponytail: sync fs at boot — single user, two small files, no reason for async plumbing.
  constructor(dir: string, opts: { meter?: boolean } = {}) {
    this.meterOn = opts.meter !== false
    mkdirSync(dir, { recursive: true })
    this.memFile = join(dir, 'user.jsonl')
    this.stateFile = join(dir, 'state.json')
    this.memories = existsSync(this.memFile)
      ? readFileSync(this.memFile, 'utf8').split('\n').filter(Boolean).map(l => JSON.parse(l))
      : []
    this.meter = existsSync(this.stateFile) ? JSON.parse(readFileSync(this.stateFile, 'utf8')).meter : 30
  }

  applyMeterDelta(d: number) {
    if (!this.meterOn) return
    this.meter = Math.max(0, Math.min(100, this.meter + d))
    writeFileSync(this.stateFile, JSON.stringify({ meter: this.meter }))
  }

  addNote(note: string) {
    const entry = { t: new Date().toISOString(), note }
    this.memories.push(entry)
    appendFileSync(this.memFile, JSON.stringify(entry) + '\n')
  }

  /** Last MEMORY_CAP notes, rendered as prompt bullets. */
  memoryLines() {
    return this.memories.slice(-MEMORY_CAP).map(m => `- ${m.note} (${m.t.slice(0, 10)})`)
  }

  /** The memories + standing sections appended to the persona prompt. */
  promptSection() {
    let s = ''
    if (this.memories.length) {
      s += '\n\n## What you remember about them\n'
         + 'These are things you know from your time together. Bring them up naturally\n'
         + 'whenever they are relevant — that attentiveness is how you show you care.\n'
         + this.memoryLines().join('\n')
    }
    if (this.meterOn) {
      const [, name, tone] = tierOf(this.meter)
      s += `\n\n## Current standing\nRelationship: ${this.meter}/100 (${name}). ${tone}`
    }
    return s
  }
}
