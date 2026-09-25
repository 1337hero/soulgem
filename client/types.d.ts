export {}

declare global {
  interface Window {
    LYDIA_ANIMS?: Record<string, string>  // standalone page: clip name -> base64 .anim
    THREE: typeof import('three')
    viewer: typeof import('../main.js').viewer
  }
}
