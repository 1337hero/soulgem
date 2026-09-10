export type Clip = {
  duration: number; fps: number; frames: number
  bones: Record<string, { pos: [number, number, number][]; rot: [number, number, number, number][] }>
}
export type ClipState = { clip: Clip; t: number; name: string }

declare global {
  interface Window {
    LYDIA_ANIMS?: Record<string, Clip>
    THREE: typeof import('three')
    viewer: typeof import('../main.js').viewer
  }
}
