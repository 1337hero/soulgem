// Durable single-user state: cortex-lite memory (SQLite+FTS+embeddings) + relationship meter.
// Meter stays in state.json. Notes live in cortex.db; legacy user.jsonl is migrated once then deleted.
// Hybrid recall: FTS5 ∪ MiniLM vectors (llama-swap) fused with RRF.
import { Database } from 'bun:sqlite'
import { existsSync, mkdirSync, readFileSync, writeFileSync, unlinkSync } from 'fs'
import { join } from 'path'
import {
  type EmbedFn, embedText, packEmbedding, unpackEmbedding, cosine, rrfFuse, EMBED_DIMS,
} from './embed.ts'

export type Note = {
  id: number
  t: string
  note: string
  type: string
  emotion: string
  intensity: number
  importance: number
}

export type NoteInput = {
  emotion?: string | null
  meterDelta?: number | null
}

// Soul-agnostic defaults; a soul overrides the prose (or names) via config.json
// "tiers" — e.g. Lydia's housecarl formality lives in HER config, not here.
export const METER_TIERS: [number, string, string][] = [
  [0,  'wary',    'You barely know this person. Guarded, reserved, giving little away.'],
  [20, 'neutral', 'Civil and comfortable enough, but you keep a little distance.'],
  [50, 'warm',    'Earned trust. Relaxed around them, openly friendly.'],
  [75, 'devoted', 'Deep attachment, freely chosen. They matter to you and it shows.'],
]
export const tierOf = (m: number, tiers = METER_TIERS) => tiers.findLast(([min]) => m >= min)!

const MEMORY_CAP = 40
const RECALL_K = 5
const DECAY_FACTOR = 0.95
const DECAY_DAYS = 14
const INTENSITY_FLOOR = 4  // somatic: never decay
const VEC_MIN_SCORE = 0.25  // drop weak vector hits before RRF

const TYPE_RULES: [string, RegExp][] = [
  ['relationship', /\b(wife|husband|partner|daughter|daughters|son|family|married|spouse|kids|children)\b/i],
  ['preference', /\b(prefer|prefers|likes?|loves?|hates?|values?|can't stand|no patience|wants?|enjoys?)\b/i],
  ['identity', /\b(calls? himself|calls? herself|my name|named|is called|i am|i'm a|works? as|builder|adhd)\b/i],
  ['event', /\b(introduced|asked|told|warned|took me|last (week|night|time)|tonight|today|yesterday|visited|went)\b/i],
]

/** Crude type from note text — good enough until the model emits types. */
export function classifyNote(text: string): string {
  for (const [type, re] of TYPE_RULES) {
    if (re.test(text)) return type
  }
  return 'insight'
}

const EMOTION_INTENSITY: Record<string, number> = {
  neutral: 1,
  warm: 2,
  teasing: 2,
  amused: 2,
  concerned: 3,
  annoyed: 3,
  proud: 3,
}

export function intensityFrom(opts: NoteInput = {}): number {
  let n = EMOTION_INTENSITY[opts.emotion ?? ''] ?? 2
  const d = opts.meterDelta ?? 0
  if (d >= 5) n = Math.max(n, 4)
  else if (d >= 3) n = Math.max(n, 3)
  else if (d <= -5) n = Math.max(n, 4)
  else if (d <= -3) n = Math.max(n, 3)
  return Math.max(0, Math.min(5, n))
}

function escapeFts(q: string): string {
  // FTS5: strip operators, quote tokens so "Mike's" / punctuation don't break MATCH
  return q
    .replace(/["*():^]/g, ' ')
    .split(/\s+/)
    .filter(t => t.length > 1)
    .map(t => `"${t}"`)
    .join(' OR ')
}

/** Lowercase, strip trailing (YYYY-MM-DD), strip punctuation — for dedupe compare. */
export function normalizeNote(text: string): string {
  return text
    .toLowerCase()
    .replace(/\s*\(\d{4}-\d{2}-\d{2}\)\s*$/g, '')
    .replace(/[^\p{L}\p{N}\s]+/gu, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

function tokenSet(text: string): Set<string> {
  return new Set(normalizeNote(text).split(' ').filter(t => t.length > 2))
}

/**
 * 0..1 similarity: exact normalize, containment (shorter/longer), or Jaccard on tokens.
 * Dedupe threshold lives in consolidate().
 */
export function noteSimilarity(a: string, b: string): number {
  const na = normalizeNote(a)
  const nb = normalizeNote(b)
  if (!na || !nb) return 0
  if (na === nb) return 1
  // One note is a more-detailed version of the other ("secret kink" ⊂ full kink note).
  // Treat as near-dupe so consolidate keeps the longer/richer survivor.
  if (na.includes(nb) || nb.includes(na)) return 0.95
  const A = tokenSet(a)
  const B = tokenSet(b)
  if (!A.size || !B.size) return 0
  let inter = 0
  for (const t of A) if (B.has(t)) inter++
  return inter / (A.size + B.size - inter)
}

const SIM_THRESHOLD = 0.72

const SCHEMA = `
CREATE TABLE IF NOT EXISTS notes (
  id INTEGER PRIMARY KEY,
  t TEXT NOT NULL,
  note TEXT NOT NULL,
  type TEXT NOT NULL DEFAULT 'insight',
  emotion TEXT NOT NULL DEFAULT '',
  intensity INTEGER NOT NULL DEFAULT 2,
  importance REAL NOT NULL DEFAULT 0.5,
  accessed_at TEXT,
  access_count INTEGER NOT NULL DEFAULT 0,
  suppressed_at TEXT,
  embedding BLOB
);
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
  note,
  content='notes',
  content_rowid='id',
  tokenize='porter'
);
CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
  INSERT INTO notes_fts(rowid, note) VALUES (new.id, new.note);
END;
CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
  INSERT INTO notes_fts(notes_fts, rowid, note) VALUES ('delete', old.id, old.note);
END;
CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE OF note ON notes BEGIN
  INSERT INTO notes_fts(notes_fts, rowid, note) VALUES ('delete', old.id, old.note);
  INSERT INTO notes_fts(rowid, note) VALUES (new.id, new.note);
END;
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

export class Store {
  dir: string
  db: Database
  stateFile: string
  meter: number
  meterOn: boolean
  tiers: [number, string, string][]
  embed: EmbedFn
  /** True when notes changed since last bulletin regen. */
  dirty: boolean

  constructor(dir: string, opts: {
    meter?: boolean
    tiers?: [number, string, string][]
    /** Inject for tests; default hits llama-swap MiniLM-L6. */
    embed?: EmbedFn
  } = {}) {
    this.dir = dir
    this.meterOn = opts.meter !== false
    this.tiers = opts.tiers ?? METER_TIERS
    this.embed = opts.embed ?? embedText
    this.dirty = false
    mkdirSync(dir, { recursive: true })
    this.stateFile = join(dir, 'state.json')
    this.meter = existsSync(this.stateFile)
      ? JSON.parse(readFileSync(this.stateFile, 'utf8')).meter
      : 30

    this.db = new Database(join(dir, 'cortex.db'))
    this.db.exec('PRAGMA journal_mode=WAL')
    this.db.exec('PRAGMA foreign_keys=ON')
    this.db.exec(SCHEMA)
    // existing DBs predate the embedding column
    try { this.db.exec('ALTER TABLE notes ADD COLUMN embedding BLOB') } catch { /* already there */ }
    this.migrateJsonl()
  }

  close() {
    this.db.close()
  }

  /** Import legacy user.jsonl once, then delete it. */
  private migrateJsonl() {
    const path = join(this.dir, 'user.jsonl')
    if (!existsSync(path)) return
    const count = this.db.query('SELECT COUNT(*) AS n FROM notes').get() as { n: number }
    const text = readFileSync(path, 'utf8')
    if (count.n === 0 && text.trim()) {
      const insert = this.db.prepare(
        `INSERT INTO notes (t, note, type, emotion, intensity, importance)
         VALUES ($t, $note, $type, '', $intensity, $importance)`)
      const tx = this.db.transaction((lines: string[]) => {
        for (const line of lines) {
          if (!line.trim()) continue
          const row = JSON.parse(line) as { t?: string; note?: string }
          if (!row.note) continue
          const type = classifyNote(row.note)
          const intensity = 2
          insert.run({
            $t: row.t ?? new Date().toISOString(),
            $note: row.note,
            $type: type,
            $intensity: intensity,
            $importance: intensity / 5,
          })
        }
      })
      tx(text.split('\n'))
      this.dirty = true
    }
    unlinkSync(path)
  }

  applyMeterDelta(d: number) {
    if (!this.meterOn) return
    this.meter = Math.max(0, Math.min(100, this.meter + d))
    writeFileSync(this.stateFile, JSON.stringify({ meter: this.meter }))
  }

  async addNote(note: string, opts: NoteInput = {}) {
    const text = note.trim()
    if (!text) return
    const type = classifyNote(text)
    const intensity = intensityFrom(opts)
    const importance = Math.max(0.15, Math.min(1, intensity / 5))
    const result = this.db.prepare(
      `INSERT INTO notes (t, note, type, emotion, intensity, importance)
       VALUES ($t, $note, $type, $emotion, $intensity, $importance)`,
    ).run({
      $t: new Date().toISOString(),
      $note: text,
      $type: type,
      $emotion: opts.emotion ?? '',
      $intensity: intensity,
      $importance: importance,
    })
    this.dirty = true
    this.setMeta('dirty', '1')
    const id = Number(result.lastInsertRowid)
    const vec = await this.embed(text)
    if (vec) this.setEmbedding(id, vec)
  }

  setEmbedding(id: number, vec: Float32Array) {
    if (vec.length !== EMBED_DIMS) return
    this.db.prepare('UPDATE notes SET embedding = ? WHERE id = ?')
      .run(packEmbedding(vec), id)
  }

  /** Embed any active notes still missing a vector. Returns count filled. */
  async backfillEmbeddings(): Promise<number> {
    const rows = this.db.query(
      `SELECT id, note FROM notes WHERE suppressed_at IS NULL AND embedding IS NULL`,
    ).all() as { id: number; note: string }[]
    let n = 0
    for (const r of rows) {
      const vec = await this.embed(r.note)
      if (!vec) continue
      this.setEmbedding(r.id, vec)
      n++
    }
    return n
  }

  get memories(): Note[] {
    return this.db.query(
      `SELECT id, t, note, type, emotion, intensity, importance
       FROM notes WHERE suppressed_at IS NULL ORDER BY id`,
    ).all() as Note[]
  }

  /** Last MEMORY_CAP active notes (fallback when no bulletin yet). */
  memoryLines(): string[] {
    const rows = this.db.query(
      `SELECT note, t FROM notes
       WHERE suppressed_at IS NULL
       ORDER BY id DESC LIMIT ?`,
    ).all(MEMORY_CAP) as { note: string; t: string }[]
    return rows.reverse().map(m => `- ${m.note} (${m.t.slice(0, 10)})`)
  }

  getBulletin(): string | null {
    return this.getMeta('bulletin')
  }

  setBulletin(text: string) {
    this.setMeta('bulletin', text.trim())
    this.setMeta('bulletin_at', new Date().toISOString())
    this.setMeta('dirty', '0')
    this.dirty = false
  }

  isDirty(): boolean {
    if (this.dirty) return true
    return this.getMeta('dirty') === '1'
  }

  /** Notes for bulletin synthesis — active, importance-weighted, capped. */
  notesForBulletin(limit = 40): Note[] {
    return this.db.query(
      `SELECT id, t, note, type, emotion, intensity, importance
       FROM notes WHERE suppressed_at IS NULL
       ORDER BY intensity DESC, importance DESC, id DESC
       LIMIT ?`,
    ).all(limit) as Note[]
  }

  /** FTS top-k ids (best first). Empty on failure / no match. */
  private ftsIds(userText: string, k: number): number[] {
    const q = escapeFts(userText)
    if (!q) return []
    try {
      const rows = this.db.query(
        `SELECT n.id FROM notes_fts f
         JOIN notes n ON n.id = f.rowid
         WHERE notes_fts MATCH ? AND n.suppressed_at IS NULL
         ORDER BY f.rank LIMIT ?`,
      ).all(q, k) as { id: number }[]
      return rows.map(r => r.id)
    } catch {
      return []
    }
  }

  /** Brute-force cosine over stored vectors (fine at note counts ≪ 1k). */
  private vectorIds(query: Float32Array, k: number): number[] {
    const rows = this.db.query(
      `SELECT id, embedding FROM notes
       WHERE suppressed_at IS NULL AND embedding IS NOT NULL`,
    ).all() as { id: number; embedding: Uint8Array }[]
    const scored: { id: number; s: number }[] = []
    for (const r of rows) {
      const v = unpackEmbedding(r.embedding)
      if (!v) continue
      const s = cosine(query, v)
      if (s >= VEC_MIN_SCORE) scored.push({ id: r.id, s })
    }
    scored.sort((a, b) => b.s - a.s)
    return scored.slice(0, k).map(x => x.id)
  }

  /**
   * Per-turn hybrid recall (FTS ∪ MiniLM, RRF). Touches access on hits.
   * Returns a prompt section or '' if nothing matched.
   */
  async recallSection(userText: string, k = RECALL_K): Promise<string> {
    if (!userText.trim()) return ''
    const fts = this.ftsIds(userText, k * 2)
    let vec: number[] = []
    const qemb = await this.embed(userText)
    if (qemb) vec = this.vectorIds(qemb, k * 2)

    const fused = (fts.length || vec.length)
      ? rrfFuse([fts, vec].filter(l => l.length)).slice(0, k)
      : []
    if (!fused.length) return ''

    const placeholders = fused.map(() => '?').join(',')
    const byId = new Map(
      (this.db.query(
        `SELECT id, t, note, type, emotion, intensity, importance
         FROM notes WHERE id IN (${placeholders})`,
      ).all(...fused) as Note[]).map(n => [n.id, n]),
    )
    const rows = fused.map(id => byId.get(id)).filter(Boolean) as Note[]
    if (!rows.length) return ''

    const now = new Date().toISOString()
    const touch = this.db.prepare(
      `UPDATE notes SET accessed_at = $t, access_count = access_count + 1 WHERE id = $id`)
    const tx = this.db.transaction((ids: number[]) => {
      for (const id of ids) touch.run({ $t: now, $id: id })
    })
    tx(rows.map(r => r.id))
    return '\n\n## Recalled for this moment\n'
      + 'Facts that may matter right now — use only if relevant:\n'
      + rows.map(r => `- (${r.type}) ${r.note}`).join('\n')
  }

  /** Decay low-intensity, untouched, older notes. High intensity / identity / relationship exempt. */
  decay(): number {
    const cutoff = new Date(Date.now() - DECAY_DAYS * 864e5).toISOString()
    const now = new Date().toISOString()
    const faded = this.db.prepare(
      `UPDATE notes SET importance = MAX(0.05, importance * $f)
       WHERE suppressed_at IS NULL
         AND intensity < $floor
         AND type NOT IN ('identity', 'relationship')
         AND (accessed_at IS NULL OR accessed_at < $cutoff)
         AND t < $cutoff`,
    ).run({ $f: DECAY_FACTOR, $floor: INTENSITY_FLOOR, $cutoff: cutoff })
    this.db.prepare(
      `UPDATE notes SET suppressed_at = $now
       WHERE suppressed_at IS NULL
         AND importance < 0.08
         AND intensity < $floor
         AND type NOT IN ('identity', 'relationship')
         AND (accessed_at IS NULL OR accessed_at < $cutoff)
         AND t < $cutoff`,
    ).run({ $now: now, $floor: INTENSITY_FLOOR, $cutoff: cutoff })
    return faded.changes
  }

  /**
   * Soft-delete near-duplicate notes. Keeps the best of each cluster
   * (intensity → importance → length → newest id). Returns count suppressed.
   */
  consolidate(): number {
    const rows = this.memories
    if (rows.length < 2) return 0
    const score = (n: Note) =>
      n.intensity * 1e9 + n.importance * 1e6 + n.note.length * 1e3 + n.id
    // best-first: first member of a similarity cluster wins
    const ranked = [...rows].sort((a, b) => score(b) - score(a))
    const survivors: Note[] = []
    const drop: number[] = []
    for (const cand of ranked) {
      if (survivors.some(s => noteSimilarity(s.note, cand.note) >= SIM_THRESHOLD)) {
        drop.push(cand.id)
      } else {
        survivors.push(cand)
      }
    }
    if (!drop.length) return 0
    const now = new Date().toISOString()
    const suppress = this.db.prepare(
      `UPDATE notes SET suppressed_at = $now WHERE id = $id AND suppressed_at IS NULL`)
    const tx = this.db.transaction((ids: number[]) => {
      for (const id of ids) suppress.run({ $now: now, $id: id })
    })
    tx(drop)
    this.dirty = true
    this.setMeta('dirty', '1')
    return drop.length
  }

  /** Always-on system-prompt memory: bulletin (or raw lines) + meter. */
  promptSection() {
    let s = ''
    const bulletin = this.getBulletin()
    const lines = this.memoryLines()
    if (bulletin) {
      s += '\n\n## What you remember about them\n'
        + 'This is your working memory of them — synthesized from everything you know.\n'
        + 'Bring facts up naturally when relevant.\n\n'
        + bulletin
    } else if (lines.length) {
      s += '\n\n## What you remember about them\n'
        + 'These are things you know from your time together. Bring them up naturally\n'
        + 'whenever they are relevant — that attentiveness is how you show you care.\n'
        + lines.join('\n')
    }
    if (this.meterOn) {
      const [, name, tone] = tierOf(this.meter, this.tiers)
      s += `\n\n## Current standing\nRelationship: ${this.meter}/100 (${name}). ${tone}`
    }
    return s
  }

  private getMeta(key: string): string | null {
    const row = this.db.query('SELECT value FROM meta WHERE key = ?').get(key) as { value: string } | null
    return row?.value ?? null
  }

  private setMeta(key: string, value: string) {
    this.db.prepare(
      `INSERT INTO meta (key, value) VALUES (?, ?)
       ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
    ).run(key, value)
  }
}
