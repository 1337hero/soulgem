// Demo capture: this tab's rendered output (avatar + overlay, no browser
// chrome) with her audio and the mic mixed into one track. Toggled from the
// UI; the file downloads when it stops.

const MIME_CANDIDATES = [
  'video/webm;codecs=h264,opus',   // plays everywhere as-is
  'video/webm;codecs=vp9,opus',
  'video/webm',
]

type Options = {
  onStart(): void
  onStop(): void
  onError(error: unknown): void
}

export function createCapture(options: Options) {
  let recorder: MediaRecorder | null = null
  let streams: MediaStream[] = []
  let chunks: Blob[] = []

  async function start() {
    const tab = await navigator.mediaDevices.getDisplayMedia({
      video: { frameRate: 60 },
      audio: true,
      // Chrome-only hints: preselect this tab, keep the audio playing locally.
      preferCurrentTab: true,
      systemAudio: 'exclude',
    } as DisplayMediaStreamOptions)
    if (tab.getAudioTracks().length === 0) {
      tab.getTracks().forEach(t => t.stop())
      throw new Error('share with "also share tab audio" checked')
    }
    const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
    streams = [tab, mic]

    const ctx = new AudioContext()
    const mix = ctx.createMediaStreamDestination()
    ctx.createMediaStreamSource(tab).connect(mix)
    ctx.createMediaStreamSource(mic).connect(mix)

    const out = new MediaStream([...tab.getVideoTracks(), ...mix.stream.getAudioTracks()])
    const mimeType = MIME_CANDIDATES.find(m => MediaRecorder.isTypeSupported(m))
    chunks = []
    recorder = new MediaRecorder(out, { mimeType, videoBitsPerSecond: 12_000_000 })
    recorder.ondataavailable = e => { if (e.data.size > 0) chunks.push(e.data) }
    recorder.onstop = () => {
      streams.forEach(s => s.getTracks().forEach(t => t.stop()))
      streams = []
      ctx.close()
      download(new Blob(chunks, { type: 'video/webm' }))
      recorder = null
      options.onStop()
    }
    // "Stop sharing" in Chrome's bar ends the take too
    tab.getVideoTracks()[0].addEventListener('ended', stop)
    recorder.start(1000)
    options.onStart()
  }

  function stop() {
    if (recorder?.state === 'recording') recorder.stop()
  }

  function toggle() {
    if (recorder) { stop(); return }
    start().catch(error => {
      streams.forEach(s => s.getTracks().forEach(t => t.stop()))
      streams = []
      options.onError(error)
    })
  }

  return { toggle, get recording() { return recorder !== null } }
}

function download(blob: Blob) {
  const stamp = new Date().toISOString().slice(0, 19).replace(/[T:]/g, '-')
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `soulgem-${stamp}.webm`
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 10_000)
}
