// OpenAI-compatible embeddings via llama-swap (MiniLM-L6).
// POST /v1/embeddings  { model, input } → { data: [{ embedding: number[] }] }

export const EMBED_URL = process.env.LYDIA_EMBED_URL
  ?? 'http://127.0.0.1:8082/v1/embeddings'
export const EMBED_MODEL = process.env.LYDIA_EMBED_MODEL ?? 'MiniLM-L6'
export const EMBED_DIMS = 384

export type EmbedFn = (text: string) => Promise<Float32Array | null>

/** Live embedder — returns null on any failure so recall can fall back to FTS. */
export async function embedText(text: string): Promise<Float32Array | null> {
  const input = text.trim()
  if (!input) return null
  try {
    const res = await fetch(EMBED_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: EMBED_MODEL, input }),
      signal: AbortSignal.timeout(30_000),
    })
    if (!res.ok) {
      console.log(`embed failed: ${res.status} ${await res.text().catch(() => '')}`)
      return null
    }
    const data = await res.json() as {
      data?: { embedding?: number[] }[]
      embedding?: number[]  // some servers flatten
    }
    const raw = data.data?.[0]?.embedding ?? data.embedding
    if (!raw?.length) return null
    if (raw.length !== EMBED_DIMS) {
      console.log(`embed dim mismatch: got ${raw.length}, expected ${EMBED_DIMS}`)
      return null
    }
    return Float32Array.from(raw)
  } catch (e) {
    console.log(`embed error: ${(e as Error).message ?? e}`)
    return null
  }
}

export function packEmbedding(v: Float32Array): Buffer {
  return Buffer.from(v.buffer, v.byteOffset, v.byteLength)
}

export function unpackEmbedding(buf: Uint8Array | Buffer | null): Float32Array | null {
  if (!buf || buf.byteLength < 4) return null
  const dims = buf.byteLength / 4
  if (dims !== EMBED_DIMS) return null
  // copy — sqlite may reuse the underlying ArrayBuffer
  const copy = new Uint8Array(buf.byteLength)
  copy.set(buf instanceof Uint8Array ? buf : new Uint8Array(buf))
  return new Float32Array(copy.buffer)
}

/** Cosine similarity. With L2-normalized vectors this is just a dot product. */
export function cosine(a: Float32Array, b: Float32Array): number {
  const n = Math.min(a.length, b.length)
  let dot = 0, na = 0, nb = 0
  for (let i = 0; i < n; i++) {
    dot += a[i] * b[i]
    na += a[i] * a[i]
    nb += b[i] * b[i]
  }
  const d = Math.sqrt(na) * Math.sqrt(nb)
  return d > 0 ? dot / d : 0
}

/**
 * Reciprocal Rank Fusion. Each list is ordered best→worst (ids).
 * Standard k=60. Returns fused ids best→worst.
 */
export function rrfFuse(lists: number[][], k = 60): number[] {
  const scores = new Map<number, number>()
  for (const list of lists) {
    list.forEach((id, rank) => {
      scores.set(id, (scores.get(id) ?? 0) + 1 / (k + rank + 1))
    })
  }
  return [...scores.entries()]
    .sort((a, b) => b[1] - a[1])
    .map(([id]) => id)
}
