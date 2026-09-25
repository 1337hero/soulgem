import * as THREE from 'three'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js'
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js'
import { GroundedSkybox } from 'three/addons/objects/GroundedSkybox.js'
import { MOOD_KEYS, VISEME_KEYS, parseServerMsg, sendClient } from './server/protocol.ts'
import { createPlayback } from './client/playback.ts'
import { replyText } from './client/captions.ts'
import { element } from './client/dom.ts'
import { bindClip, boneKey, parseClip, samplePose } from './client/anim.ts'

const canvas = element('view', 'canvas')
const renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true })
renderer.setPixelRatio(Math.min(devicePixelRatio, 2))
renderer.toneMapping = THREE.ACESFilmicToneMapping
renderer.toneMappingExposure = 1.25
renderer.outputColorSpace = THREE.SRGBColorSpace

const scene = new THREE.Scene()
scene.environment = new THREE.PMREMGenerator(renderer)
  .fromScene(new RoomEnvironment(), 0.04).texture
scene.environmentIntensity = 0.4

const camera = new THREE.PerspectiveCamera(38, 1, 0.05, 50)
camera.position.set(0.9, 1.55, 2.6)

const controls = new OrbitControls(camera, canvas)
controls.target.set(0, 1.05, 0)
controls.enableDamping = true
controls.minDistance = 0.35
controls.maxDistance = 6
controls.maxPolarAngle = Math.PI / 2 - 0.03  // never below the floor

// lighting: warm key, cool fill, rim
const key = new THREE.DirectionalLight(0xffe8c8, 2.2)
key.position.set(1.4, 2.0, 3.0)
scene.add(key)
const fill = new THREE.DirectionalLight(0xbfd4ff, 0.35)
fill.position.set(-3, 1.5, 1)
scene.add(fill)
const rim = new THREE.DirectionalLight(0xdfe8ff, 1.1)
rim.position.set(-1, 2.5, -3)
scene.add(rim)
const ambient = new THREE.AmbientLight(0x606070, 0.35)
scene.add(ambient)

// per-soul lighting override (config.json `lighting`, sent in the state msg).
// Always resets to the stock rig first so switching from a custom-lit soul to
// a stock one doesn't inherit the previous soul's light.
/** @type {{exposure: number, lights: [THREE.Light, number, number][]}} */
const STOCK_RIG = { exposure: 1.25, lights: [[ambient, 0x606070, 0.35], [key, 0xffe8c8, 2.2],
                                            [fill, 0xbfd4ff, 0.35], [rim, 0xdfe8ff, 1.1]] }
/** @param {import('./server/protocol.ts').Lighting | null} cfg */
function applyLighting(cfg) {
  renderer.toneMappingExposure = STOCK_RIG.exposure
  for (const [light, color, intensity] of STOCK_RIG.lights) {
    light.color.set(color)
    light.intensity = intensity
  }
  if (!cfg) return
  if (cfg.exposure != null) renderer.toneMappingExposure = cfg.exposure
  for (const [name, light] of Object.entries({ ambient, key, fill, rim })) {
    const c = cfg[/** @type {'ambient' | 'key' | 'fill' | 'rim'} */ (name)]
    if (!c) continue
    if (c.color != null) light.color.set(c.color)
    if (c.intensity != null) light.intensity = c.intensity
  }
}

// soft contact shadow under her (radial gradient, works in void and in scenes)
const shadowCanvas = document.createElement('canvas')
shadowCanvas.width = shadowCanvas.height = 256
const sctx = shadowCanvas.getContext('2d')
if (!sctx) throw new Error('2D canvas is unavailable')
const grad = sctx.createRadialGradient(128, 128, 20, 128, 128, 128)
grad.addColorStop(0, 'rgba(0,0,0,0.45)')
grad.addColorStop(1, 'rgba(0,0,0,0)')
sctx.fillStyle = grad
sctx.fillRect(0, 0, 256, 256)
const ground = new THREE.Mesh(
  new THREE.CircleGeometry(0.85, 64),
  new THREE.MeshBasicMaterial({ map: new THREE.CanvasTexture(shadowCanvas),
                                transparent: true, depthWrite: false }))
ground.rotation.x = -Math.PI / 2
ground.position.y = 0.01
scene.add(ground)

// ---- scene (equirect pano projected onto a grounded skybox) ----
/** @type {GroundedSkybox | null} */
let sky = null
/** @param {string | null} url
 * @param {{height?: number, radius?: number}} opts */
async function setScene(url, opts = {}) {
  if (sky) { scene.remove(sky); sky.geometry.dispose(); sky = null }
  if (!url) {  // back to the void
    scene.background = null
    scene.environment = new THREE.PMREMGenerator(renderer)
      .fromScene(new RoomEnvironment(), 0.04).texture
    return
  }
  const tex = await new Promise((res, rej) => new THREE.TextureLoader().load(url, res, undefined, rej))
  tex.mapping = THREE.EquirectangularReflectionMapping
  tex.colorSpace = THREE.SRGBColorSpace
  // height = pano capture height, radius = room scale — per-scene tunables
  sky = new GroundedSkybox(tex, opts.height ?? 1.6, opts.radius ?? 12)
  sky.position.y = (opts.height ?? 1.6) - 0.01
  scene.add(sky)
  scene.environment = tex
}

/** @type {THREE.Group | null} */
let model = null
/** @type {THREE.Mesh | null} */
let head = null          // skinned mesh with morph targets
/** @type {THREE.Bone | null} */
let headBone = null
/** @type {THREE.Bone | null} */
let spineBone = null
const headBindWorldQ = new THREE.Quaternion()
const spineBindQ = new THREE.Quaternion()

/** @param {string} url */
function loadBody(url) {
  element('loading', 'div').style.display = ''
  new GLTFLoader().load(url, gltf => {
  if (model) {
    // soul switch: drop the old body wholesale (GPU buffers included)
    scene.remove(model)
    model.traverse(o => {
      if (!(o instanceof THREE.Mesh)) return
      o.geometry.dispose()
      for (const m of Array.isArray(o.material) ? o.material : [o.material]) {
        for (const value of Object.values(m)) if (value instanceof THREE.Texture) value.dispose()
        m.dispose()
      }
    })
    head = headBone = spineBone = null
    for (const k in boneByKey) delete boneByKey[k]
  }
  model = gltf.scene
  model.traverse(o => {
    if (!(o instanceof THREE.Mesh)) return
    for (const m of Array.isArray(o.material) ? o.material : [o.material]) {
    if (!(m instanceof THREE.MeshStandardMaterial)) continue
    m.envMapIntensity = 0.8
    const name = o.name.toLowerCase()
    if (name.includes('hair')) {
      // hair: alpha-mask with depth write — blended strips sort-shred badly
      m.transparent = false
      m.depthWrite = true
      m.alphaTest = 0.42
      m.roughness = 0.9
      m.envMapIntensity = 0.05
      m.emissive.copy(m.color).multiplyScalar(0.08)  // lift unlit backface strands
    } else if (name.includes('eyes')) {
      m.roughness = 0.1
    } else {
      m.roughness = 0.75
      m.envMapIntensity = 0.6
    }
    }
  })
  scene.add(model)
  model.traverse(o => {
    if (o instanceof THREE.Mesh && o.morphTargetDictionary && Object.keys(o.morphTargetDictionary).length) head = o
    if (o instanceof THREE.Bone && o.name === 'NPC_Head_Head') headBone = o
    if (o instanceof THREE.Bone && o.name === 'NPC_Spine2_Spn2') spineBone = o
    if (o instanceof THREE.Bone) boneByKey[boneKey(o.name)] = o
  })
  bound = new WeakMap()  // new skeleton: rebind every clip on next sample
  // drift guard: a morph name the GLB lacks silently no-ops forever
  const missing = [...VISEME_KEYS, ...MOOD_KEYS].filter(k => head?.morphTargetDictionary?.[k] === undefined)
  if (missing.length) console.warn('GLB is missing morph targets:', missing.join(', '))
  model.updateMatrixWorld(true)
  if (headBone) headBone.getWorldQuaternion(headBindWorldQ)
  if (spineBone) spineBindQ.copy(spineBone.quaternion)
  element('loading', 'div').style.display = 'none'
  }, undefined, e => {
    console.error('body load failed:', e)
    element('loading', 'div').textContent = 'body failed to load'
  })
}
loadBody('body.glb')

/** @param {string} name
 * @param {number} v */
function setMorph(name, v) {
  const i = head?.morphTargetDictionary?.[name]
  if (head?.morphTargetInfluences && i !== undefined) head.morphTargetInfluences[i] = v
}

// ---- idle animation playback (decoded HKX clips) ----
/** @type {Record<string, THREE.Bone>} */
const boneByKey = {}          // boneKey(name) -> Bone
/** @type {WeakMap<import('./client/anim.ts').Clip, import('./client/anim.ts').BoundTrack[]>} */
let bound = new WeakMap()     // clip -> tracks resolved against the current body
/** @type {Record<string, import('./client/anim.ts').Clip>} */
const clips = {}              // name -> clip
/** @type {string[]} */
const clipNames = []          // idle rotation pool
/** @type {import('./client/anim.ts').ClipState | null} */
let cur = null                // { clip, t }
/** @type {import('./client/anim.ts').ClipState | null} */
let prev = null               // fading-out clip during crossfade
let fade = 1                  // 0..1 crossfade progress
const FADE_DUR = 0.6
let switchAt = Infinity
const timer = new THREE.Timer()

// idle pool: everything except one-shot gestures, talking body language, and
// dance clips (dances only play on request, never in the idle rotation)
/** @param {string} name */
const isIdleClip = name => !/^(gesture|talk|dance)_/.test(name)

/** @param {string} name
 * @param {import('./client/anim.ts').Clip} clip */
function addClip(name, clip) {
  clips[name] = clip
  if (!isIdleClip(name)) return
  clipNames.push(name)
  if (!cur) {  // the first idle to arrive starts her moving
    cur = { clip, t: 0, name }
    scheduleSwitch()
  }
}

/** @param {string[]} names */
function fetchClips(names) {
  return Promise.all(names.map(async name => {
    try {
      const res = await fetch(`anims/${name}.anim`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      addClip(name, parseClip(await res.arrayBuffer()))
    } catch (e) { console.warn(`animation ${name} failed to load:`, e) }
  }))
}

async function loadAnims() {
  if (window.LYDIA_ANIMS) {
    for (const [name, b64] of Object.entries(window.LYDIA_ANIMS)) addClip(name, parseClip(b64ToArrayBuffer(b64)))
    return
  }
  /** @type {string[]} */
  const names = await (await fetch('anims/index.json')).json()
  // idles first so her first pose isn't queued behind dances; the rest stream in
  await fetchClips(names.filter(isIdleClip))
  await fetchClips(names.filter(n => !isIdleClip(n)))
}
loadAnims().catch(e => console.warn('animations failed to load:', e))

function scheduleSwitch() {
  switchAt = timer.getElapsed() + 15 + Math.random() * 15
}

/** @param {string} name */
function playIdle(name) {
  if (!clips[name] || (cur && clips[name] === cur.clip)) return
  prev = cur
  fade = 0
  cur = { clip: clips[name], t: 0, name }
}

// one-shot gesture: crossfade in, play once, crossfade back to the idle
/** @type {string | null} */
let gestureReturn = null

/** @param {string} name */
function playGesture(name) {
  if (!clips[name] || gestureReturn) return
  gestureReturn = cur?.name ?? clipNames[0]
  playIdle(name)
}

// ---- dance: a looping dance clip + its music track, on request ----
// Unlike a one-shot gesture the dance loops (via the sampler's modulo wrap)
// and carries audio; it runs until the cap, the track ends, or a barge-in.
const DANCE_MS = 40000        // cap so a full-length track can't hijack the scene
const DUCK_VOL = 0.28, FULL_VOL = 0.85
let dancing = false
/** @type {HTMLAudioElement | null} */
let danceMusic = null         // the live HTMLAudioElement, kept for ducking + fade
/** @type {ReturnType<typeof setTimeout> | null} */
let danceTimer = null

function startDance() {
  const names = Object.keys(clips).filter(n => n.startsWith('dance_'))
  if (!names.length) return
  const name = names[Math.floor(Math.random() * names.length)]
  stopDance(false)            // clear any dance already running
  dancing = true
  gestureReturn = null        // a dance overrides any pending gesture return
  playIdle(name)              // crossfade in; the clip loops on its own
  danceMusic = new Audio(`music/${name}.ogg`)   // dance_1 clip <-> dance_1.ogg
  danceMusic.volume = speech.speaking ? DUCK_VOL : FULL_VOL
  danceMusic.onended = () => stopDance()
  danceMusic.play().catch(() => {})             // no music if autoplay is blocked
  danceTimer = setTimeout(() => stopDance(), DANCE_MS)
}

function stopDance(fade = true) {
  if (danceTimer) clearTimeout(danceTimer)
  const m = danceMusic
  danceMusic = null
  const wasDancing = dancing
  dancing = false
  if (m) {
    m.onended = null
    if (fade) {
      const id = setInterval(() => {
        m.volume = Math.max(0, m.volume - 0.06)
        if (m.volume <= 0) { clearInterval(id); m.pause() }
      }, 40)
    } else m.pause()
  }
  // hand the body back to a fresh idle
  if (wasDancing && clipNames.length) {
    playIdle(clipNames[Math.floor(Math.random() * clipNames.length)])
    scheduleSwitch()
  }
}

const _qa = new THREE.Quaternion()

/** @param {import('./client/anim.ts').Clip} clip */
function tracksFor(clip) {
  let tracks = bound.get(clip)
  if (!tracks) bound.set(clip, tracks = bindClip(clip, boneByKey))
  return tracks
}

/** @param {import('./client/anim.ts').ClipState} state
 * @param {number} dt
 * @param {number} weight */
function sampleInto(state, dt, weight) {
  const c = state.clip
  state.t = (state.t + dt) % c.duration
  const f = state.t * c.fps
  const f0 = Math.floor(f) % c.frames
  const f1 = (f0 + 1) % c.frames
  samplePose(tracksFor(c), c.data, f0, f1, f - Math.floor(f), weight)
}

/** @param {number} dt
 * @param {number} t */
function updateIdle(dt, t) {
  if (!cur) return false
  if (gestureReturn && cur.t + dt >= cur.clip.duration - Math.min(FADE_DUR, cur.clip.duration / 3)) {
    const back = gestureReturn
    gestureReturn = null
    playIdle(back)
  }
  if (t >= switchAt && clipNames.length > 1 && !gestureReturn && !dancing) {
    const others = clipNames.filter(n => clips[n] !== cur?.clip)
    playIdle(others[Math.floor(Math.random() * others.length)])
    scheduleSwitch()
  }
  if (prev && fade < 1) {
    fade = Math.min(1, fade + dt / FADE_DUR)
    sampleInto(prev, dt, 1)        // write prev pose
    sampleInto(cur, dt, fade)      // blend current over it
    if (fade >= 1) prev = null
  } else {
    sampleInto(cur, dt, 1)
  }
  return true
}

// ---- procedural life ----
const _headPos = new THREE.Vector3()

let blinkAt = 2.0     // next blink time
let blinkT = -1       // progress through current blink, -1 = idle
const BLINK_DUR = 0.22

/** @param {number} t */
function blinkCurve(t) {  // 0..1 -> lid closure, fast close slow open
  return t < 0.4 ? t / 0.4 : 1 - (t - 0.4) / 0.6
}

let spin = false
const alive = true
controls.addEventListener('start', () => { spin = false })
const spinBtn = element('spin', 'button')
spinBtn.textContent = 'rotate'
spinBtn.addEventListener('click', e => {
  spin = !spin
  spinBtn.textContent = spin ? 'pause' : 'rotate'
})

const look = { yaw: 0, pitch: 0 }
// live-tunable via console: viewer.gaze.pitchBias = -0.08 (negative = aim higher)
const gaze = { yawMax: 0.7, pitchMax: 0.35, pitchBias: -0.06 }
const _pq = new THREE.Quaternion()
const _target = new THREE.Quaternion()
const _e = new THREE.Euler()

window.THREE = THREE  // console/scene experiments
export const viewer = { camera, controls, scene, renderer, setMorph, playIdle, playGesture, setScene, gaze, applyLighting,
  get head() { return head }, get clips() { return clips },
  /** @param {string} text */
  say: text => sendTurn({ type: 'text', text }), startDance, stopDance }
window.viewer = viewer

renderer.setAnimationLoop(() => {
  resize()
  timer.update()
  const dt = Math.min(timer.getDelta(), 0.1)
  const t = timer.getElapsed()

  if (spin && model) model.rotation.y += 0.004

  if (alive && model) {
    updateMouth()
    updateDanceVolume(dt)
    const hasIdle = updateIdle(dt, t)
    // breathing fallback when no idle clip drives the spine
    if (!hasIdle && spineBone) {
      _e.set(Math.sin(t * 1.5) * 0.010, 0, 0)
      spineBone.quaternion.copy(spineBindQ).multiply(_target.setFromEuler(_e))
    }
    updateGaze(dt, t)
    updateBlink(dt, t)
  }

  controls.update()
  renderer.render(scene, camera)
})

/** @param {number} dt */
function updateDanceVolume(dt) {
  // duck the dance music under her voice, ease it back up once she stops
  if (dancing && danceMusic) {
    const target = speech.speaking ? DUCK_VOL : FULL_VOL
    danceMusic.volume += (target - danceMusic.volume) * (1 - Math.exp(-dt * 4))
  }
}

/** @param {number} dt
 * @param {number} t */
function updateGaze(dt, t) {
  // look-at camera (eye contact with the viewer) layered over the head pose
  if (headBone && !spin) {
    const k = 1 - Math.exp(-dt * 6)
    headBone.getWorldPosition(_headPos)
    const dx = camera.position.x - _headPos.x
    const dy = camera.position.y - _headPos.y
    const dz = camera.position.z - _headPos.z
    const yawT = Math.atan2(dx, dz)
    const pitchT = -Math.atan2(dy, Math.hypot(dx, dz)) + gaze.pitchBias
    look.yaw += (THREE.MathUtils.clamp(yawT, -gaze.yawMax, gaze.yawMax) - look.yaw) * k
    look.pitch += (THREE.MathUtils.clamp(pitchT, -gaze.pitchMax, gaze.pitchMax) - look.pitch) * k
    _e.set(look.pitch + Math.sin(t * 0.47) * 0.01,
           look.yaw + Math.sin(t * 0.31) * 0.015 + Math.sin(t * 0.73) * 0.01, 0)
    headBone.parent?.getWorldQuaternion(_pq)
    const animWorld = _qa.copy(_pq).multiply(headBone.quaternion)
    _target.setFromEuler(_e).multiply(animWorld)
    headBone.quaternion.copy(_pq.invert().multiply(_target))
  }
}

/** @param {number} dt
 * @param {number} t */
function updateBlink(dt, t) {
  // blink
  if (head) {
    if (blinkT < 0 && t >= blinkAt) blinkT = 0
    if (blinkT >= 0) {
      blinkT += dt
      const v = blinkT >= BLINK_DUR ? 0 : blinkCurve(blinkT / BLINK_DUR)
      setMorph('BlinkLeft', v)
      setMorph('BlinkRight', v)
      if (blinkT >= BLINK_DUR) {
        blinkT = -1
        blinkAt = t + 2 + Math.random() * 4
        if (Math.random() < 0.12) blinkAt = t + 0.25  // occasional double blink
      }
    }
  }
}

function resize() {
  const w = canvas.clientWidth, h = canvas.clientHeight
  if (canvas.width !== w * renderer.getPixelRatio() || canvas.height !== h * renderer.getPixelRatio()) {
    renderer.setSize(w, h, false)
    camera.aspect = w / h
    camera.updateProjectionMatrix()
  }
}

// ---- voice loop client (P2) ----
const talkBtn = element('talk', 'button')
const captionEl = element('caption', 'div')
let transcript = ''
let reply = ''
/** @param {string} text */
function showTranscript(text) {
  transcript = text
  reply = ''
  renderCaption()
}
/** @param {import('./client/playback.ts').SpeechChunk} msg */
function updateCaption(msg) {
  reply = replyText(reply, msg)
  renderCaption()
}
function renderCaption() {
  const you = document.createElement('span')
  you.className = 'you'
  you.textContent = transcript ? `"${transcript}"` : ''
  captionEl.replaceChildren(you, document.createTextNode(reply))
}
const meterEl = element('meter', 'div')
/** @param {number | null} meter
 * @param {string | null} tier */
const showMeter = (meter, tier) => {
  meterEl.textContent = meter == null ? '' : `regard ${meter}/100 · ${tier}`
}

// soul picker: one button per soul from /souls; clicking asks the server to
// switch (the resulting 'soul' broadcast does the actual reload, all tabs)
const soulsEl = element('souls', 'div')
/** @param {string} name */
function markActiveSoul(name) {
  for (const b of soulsEl.children) if (b instanceof HTMLElement) b.classList.toggle('active', b.dataset.name === name)
}
if (soulsEl && location.protocol !== 'file:') {
  fetch('souls').then(r => r.json()).then(souls => {
    if (souls.length < 2) return  // nothing to pick
    for (const s of souls) {
      const b = document.createElement('button')
      b.textContent = s.name
      b.dataset.name = s.name
      b.classList.toggle('active', s.active)
      b.onclick = () => ws?.readyState === WebSocket.OPEN
        && sendClient(ws, { type: 'switch_soul', name: s.name })
      soulsEl.appendChild(b)
    }
  }).catch(() => {})
}
/** @type {WebSocket | null} */
let ws = null
/** @type {MediaRecorder | null} */
let recorder = null
/** @type {Blob[]} */
let recChunks = []
/** @type {AudioContext | null} */
let audioCtx = null

// Keep the audio sink awake: PipeWire suspends idle sinks, and a woken sink
// eats the first ~0.5s of a chunk — lips lead, sound joins mid-sentence. A
// silent constant source holds the stream open from the first user gesture.
/** @type {ConstantSourceNode | null} */
let audioKeepalive = null
function ensureAudio() {
  audioCtx ??= new AudioContext()
  if (audioCtx.state === 'suspended') audioCtx.resume()
  if (!audioKeepalive) {
    const src = new ConstantSourceNode(audioCtx, { offset: 0 })
    src.connect(audioCtx.destination)
    src.start()
    audioKeepalive = src
    // one shared analyser for the jaw-flap loudness fallback — a per-chunk
    // analyser never gets disconnected and leaks one node per sentence
  }
  if (!analyser) {
    analyser = audioCtx.createAnalyser()
    analyser.fftSize = 512
    analyser.connect(audioCtx.destination)
  }
  return { context: audioCtx, output: analyser }
}
window.addEventListener('pointerdown', ensureAudio, { capture: true })
window.addEventListener('keydown', ensureAudio, { capture: true })
/** @type {AnalyserNode | null} */
let analyser = null
const speech = createPlayback({
  prepare: prepareAudio,
  onChunk(msg) { updateCaption(msg); applySpeechGesture(msg) },
  onIdle: finishSpeaking,
  onStop() {
    stopDance(false)
    setMood(null)
    talkBtn.classList.remove('busy')
  },
  onError(error) {
    captionEl.textContent = `(audio failed: ${String(error)})`
  },
})

function connectWS() {
  if (location.protocol === 'file:') return
  ws = new WebSocket(`ws://${location.host}/ws`)
  ws.onmessage = e => {
    const msg = parseServerMsg(JSON.parse(e.data))
    if (msg.type === 'state') {
      showMeter(msg.meter, msg.tier)
      applyLighting(msg.lighting)
    } else if (msg.type === 'transcript') {
      if (speech.speaking) { stopSpeaking(); talkBtn.classList.add('busy') }
      showTranscript(msg.text)
    } else if (msg.type === 'speak') {
      onSpeak(msg)
    } else if (msg.type === 'soul') {
      // active soul changed (this tab or another): drop any speech in flight,
      // reload the body; the preceding state msg already re-applied lighting
      stopSpeaking()
      showTranscript('')
      loadBody(msg.glb)
      markActiveSoul(msg.name)
    } else if (msg.type === 'visemes') {
      // late-arriving track for a chunk already sent; cue times are absolute
      // on the chunk clock, so patching mid-playback stays in sync
      speech.patchVisemes(msg.seq, msg.visemes)
    } else if (msg.type === 'speak_end') {
      speech.end()
    } else if (msg.type === 'error') {
      stopSpeaking()
      captionEl.textContent = `(${msg.error})`
      talkBtn.classList.remove('busy')
    }
  }
  ws.onclose = () => { stopSpeaking(); setTimeout(connectWS, 2000) }
}
connectWS()

/** @param {Record<string, number> | null} mood */
function setMood(mood) {
  for (const k of MOOD_KEYS) setMorph(k, mood?.[k] ?? 0)
}

function stopSpeaking() { speech.stop() }

function finishSpeaking() {
  setMood(null)
  talkBtn.classList.remove('busy')
  const finishedReply = reply
  setTimeout(() => {
    if (!speech.current && reply === finishedReply && !talkBtn.classList.contains('busy')) showTranscript('')
  }, 4000)
}

/** @param {import('./client/playback.ts').SpeechChunk} msg */
function onSpeak(msg) { void speech.enqueue(msg) }

/** @param {string} b64 */
function b64ToArrayBuffer(b64) {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes.buffer
}

/** @param {import('./client/playback.ts').SpeechChunk} msg */
function applySpeechGesture(msg) {
  if (msg.seq === 0) {
    showMeter(msg.meter, msg.tier)
    setMood(msg.mood)
    if (msg.gesture === 'dance') {
      startDance()
    } else if (msg.gesture === 'idle_switch' && clipNames.length > 1) {
      const others = clipNames.filter(n => clips[n] !== cur?.clip)
      playIdle(others[Math.floor(Math.random() * others.length)])
    } else if (msg.gesture && msg.gesture !== 'none' && clips['gesture_' + msg.gesture]) {
      playGesture('gesture_' + msg.gesture)
    }
  }
  // talking body language: a dialogue one-shot per chunk unless a gesture or
  // dance is already driving the body
  if (!gestureReturn && !dancing) {
    const angry = msg.emotion === 'annoyed' || (msg.mood?.MoodAnger ?? 0) > 0.2
    const pool = Object.keys(clips).filter(n =>
      angry ? n.startsWith('talk_angry') : (n.startsWith('talk_') && !n.startsWith('talk_angry')))
    if (pool.length && Math.random() < 0.8) playGesture(pool[Math.floor(Math.random() * pool.length)])
  }
}

/** @param {import('./client/playback.ts').SpeechChunk} msg
 * @param {() => void} ended */
async function prepareAudio(msg, ended) {
  const { context, output } = ensureAudio()
  await context.resume()
  const buf = await context.decodeAudioData(b64ToArrayBuffer(msg.audio))
  return () => {
    const src = context.createBufferSource()
    src.buffer = buf
    src.connect(output)
    src.onended = () => { src.disconnect(); ended() }
    const startedAt = context.currentTime
    src.start()
    return {
      startedAt,
      stop() { src.onended = null; src.stop(); src.disconnect() },
    }
  }
}

// mouth driver — called every rendered frame from the animation loop.
// Rhubarb cue track on the AudioContext clock; loudness jaw-flap as fallback.
const VISEME_LEAD = 0.06   // open the mouth slightly before the sound
const _fft = new Uint8Array(256)

/** @param {string} name */
function morphValue(name) {
  const index = head?.morphTargetDictionary?.[name]
  return index === undefined ? 0 : head?.morphTargetInfluences?.[index] ?? 0
}

function updateMouth() {
  if (!head) return
  if (!speech.speaking) {
    for (const k of VISEME_KEYS) {
      const curV = morphValue(k)
      if (curV > 0.001) setMorph(k, curV * 0.7)
    }
    return
  }
  const track = speech.current?.visemes
  if (track && audioCtx) {
    const t = audioCtx.currentTime - speech.startedAt + VISEME_LEAD
    const cue = track.find(c => t >= c.s && t < c.e)
    for (const k of VISEME_KEYS) {
      const curV = morphValue(k)
      const target = cue?.v === k ? (k === 'BigAah' ? 0.9 : 0.75) : 0
      const rate = target > curV ? 0.65 : 0.35   // fast attack, slower release
      setMorph(k, curV + (target - curV) * rate)
    }
  } else if (analyser) {
    analyser.getByteFrequencyData(_fft)
    let sum = 0
    for (let i = 2; i < 64; i++) sum += _fft[i]
    const level = Math.min(1, (sum / 62 / 255) * 3.2)
    const curV = morphValue('BigAah')
    setMorph('BigAah', curV + (level * 0.55 - curV) * 0.45)
  }
}

// Every turn (voice, typed, or viewer.say) goes through here: barge-in over a
// reply in flight, then send and show the pending state.
/** @param {import('./server/protocol.ts').ClientMsg} payload */
function sendTurn(payload) {
  if (!ws || ws.readyState !== 1) return false
  if (speech.speaking || talkBtn.classList.contains('busy')) {
    sendClient(ws, { type: 'interrupt' })
    stopSpeaking()
  }
  sendClient(ws, payload)
  talkBtn.classList.add('busy')
  captionEl.textContent = '…'
  return true
}

async function startRec() {
  if (!ws || ws.readyState !== 1) return
  // barge-in: talking over her (or over a pending reply) cancels the turn
  if (speech.speaking || talkBtn.classList.contains('busy')) {
    sendClient(ws, { type: 'interrupt' })
    stopSpeaking()
  }
  const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
  recChunks = []
  recorder = new MediaRecorder(stream, { mimeType: 'audio/webm;codecs=opus' })
  recorder.ondataavailable = e => recChunks.push(e.data)
  recorder.onstop = async () => {
    stream.getTracks().forEach(t => t.stop())
    const blob = new Blob(recChunks, { type: 'audio/webm' })
    if (blob.size < 2000) return  // too short
    const b64 = btoa(String.fromCharCode(...new Uint8Array(await blob.arrayBuffer())))
    // interrupt already fired in startRec; just send and mark pending
    talkBtn.classList.add('busy')
    captionEl.textContent = '…'
    if (ws?.readyState === WebSocket.OPEN) sendClient(ws, { type: 'audio', data: b64 })
  }
  recorder.start()
  talkBtn.classList.add('rec')
}

function stopRec() {
  if (recorder?.state === 'recording') recorder.stop()
  talkBtn.classList.remove('rec')
}

talkBtn.addEventListener('pointerdown', startRec)
talkBtn.addEventListener('pointerup', stopRec)
talkBtn.addEventListener('pointerleave', stopRec)

// ---- text input mode ----
// The talk button and the input swap in the same slot; 'i' opens, Esc returns
// to voice. Choice persists so a typed session survives a reload.
const textEl = element('textin', 'input')
const modeBtn = element('modeBtn', 'button')

/** @param {boolean} on */
function setTextMode(on) {
  textEl.classList.toggle('hidden', !on)
  talkBtn.classList.toggle('hidden', on)
  modeBtn.innerHTML = on ? '&#127908;' : '&#9000;'   // mic : keyboard
  localStorage.setItem('soulgem.textMode', on ? '1' : '0')
  if (on) textEl.focus()
  else textEl.blur()
}
setTextMode(localStorage.getItem('soulgem.textMode') === '1')

modeBtn.addEventListener('click', () => setTextMode(textEl.classList.contains('hidden')))

textEl.addEventListener('keydown', e => {
  if (e.key === 'Enter') {
    const text = textEl.value.trim()
    if (text && sendTurn({ type: 'text', text })) textEl.value = ''
  } else if (e.key === 'Escape') {
    setTextMode(false)
  }
})

// Push-to-talk keys must never fire while typing — 't' appears in most words.
/** @param {KeyboardEvent} e */
const typing = e => e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement
window.addEventListener('keydown', e => {
  if (typing(e)) return
  if (e.key === 't' && !e.repeat) startRec()
  else if (e.key === 'i' && !e.repeat) { e.preventDefault(); setTextMode(true) }
})
window.addEventListener('keyup', e => { if (!typing(e) && e.key === 't') stopRec() })

// "dance for me": a hidden turn — she agrees in her own words and the reply's
// gesture:'dance' kicks off the loop + music (see applySpeechGesture). Barge-in handled
// by sendTurn, so pressing it mid-speech just restarts the moment.
element('dance', 'button').addEventListener('click',
  () => sendTurn({ type: 'text', text: 'Dance for me!' }))
