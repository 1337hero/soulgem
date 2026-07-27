import * as THREE from 'three'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js'
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js'
import { MOOD_KEYS, VISEME_KEYS } from './server/protocol.ts'

const canvas = document.getElementById('view')
const renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: true })
renderer.setPixelRatio(Math.min(devicePixelRatio, 2))
renderer.toneMapping = THREE.ACESFilmicToneMapping
renderer.toneMappingExposure = 1.45
renderer.outputColorSpace = THREE.SRGBColorSpace

const scene = new THREE.Scene()
scene.environment = new THREE.PMREMGenerator(renderer)
  .fromScene(new RoomEnvironment(), 0.04).texture
scene.environmentIntensity = 1.0

const camera = new THREE.PerspectiveCamera(38, 1, 0.05, 50)
camera.position.set(0.9, 1.55, 2.6)

const controls = new OrbitControls(camera, canvas)
controls.target.set(0, 1.05, 0)
controls.enableDamping = true
controls.minDistance = 0.35
controls.maxDistance = 6

// lighting: warm key, cool fill, rim
const key = new THREE.DirectionalLight(0xfff1e0, 2.5)
key.position.set(0.6, 2.2, 3.8)
scene.add(key)
const fill = new THREE.DirectionalLight(0xbfd4ff, 1.0)
fill.position.set(-3, 1.5, 1)
scene.add(fill)
const rim = new THREE.DirectionalLight(0xdfe8ff, 1.6)
rim.position.set(-1, 2.5, -3)
scene.add(rim)
scene.add(new THREE.AmbientLight(0x606070, 1.1))

// ground disc
const ground = new THREE.Mesh(
  new THREE.CircleGeometry(0.85, 64),
  new THREE.MeshStandardMaterial({ color: 0x22242c, roughness: 0.9, metalness: 0 }))
ground.rotation.x = -Math.PI / 2
scene.add(ground)

let model = null
let head = null          // skinned mesh with morph targets
let headBone = null
let spineBone = null
const headBindWorldQ = new THREE.Quaternion()
const spineBindQ = new THREE.Quaternion()

new GLTFLoader().load('body.glb', gltf => {
  model = gltf.scene
  model.traverse(o => {
    if (!o.isMesh) return
    const m = o.material
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
  })
  scene.add(model)
  model.traverse(o => {
    if (o.isMesh && o.morphTargetDictionary && Object.keys(o.morphTargetDictionary).length) head = o
    if (o.isBone && o.name === 'NPC_Head_Head') headBone = o
    if (o.isBone && o.name === 'NPC_Spine2_Spn2') spineBone = o
    if (o.isBone) boneByKey[norm(o.name)] = o
  })
  // drift guard: a morph name the GLB lacks silently no-ops forever
  const missing = [...VISEME_KEYS, ...MOOD_KEYS].filter(k => head?.morphTargetDictionary[k] === undefined)
  if (missing.length) console.warn('GLB is missing morph targets:', missing.join(', '))
  model.updateMatrixWorld(true)
  if (headBone) headBone.getWorldQuaternion(headBindWorldQ)
  if (spineBone) spineBindQ.copy(spineBone.quaternion)
  document.getElementById('loading').style.display = 'none'
})

function setMorph(name, v) {
  if (!head) return
  const i = head.morphTargetDictionary[name]
  if (i !== undefined) head.morphTargetInfluences[i] = v
}

// ---- idle animation playback (decoded Skyrim HKX clips) ----
const boneByKey = {}          // normalized name -> Bone
const norm = s => s.replace(/[^A-Za-z0-9]/g, '')
const clips = {}              // name -> clip json
let clipNames = []
let cur = null                // { clip, t }
let prev = null               // fading-out clip during crossfade
let fade = 1                  // 0..1 crossfade progress
const FADE_DUR = 0.6
let switchAt = Infinity

async function loadAnims() {
  if (window.LYDIA_ANIMS) {
    Object.assign(clips, window.LYDIA_ANIMS)
  } else {
    try {
      const names = await (await fetch('anims/index.json')).json()
      await Promise.all(names.map(async n => {
        clips[n] = await (await fetch(`anims/${n}.json`)).json()
      }))
    } catch { return }
  }
  // idle pool: everything except one-shot gestures and talking body language
  clipNames = Object.keys(clips).filter(n => !n.startsWith('gesture_') && !n.startsWith('talk_'))
  if (clipNames.length) {
    cur = { clip: clips[clipNames[0]], t: 0, name: clipNames[0] }
    scheduleSwitch()
  }
}
loadAnims()

function scheduleSwitch() {
  switchAt = timer.getElapsed() + 15 + Math.random() * 15
}

function playIdle(name) {
  if (!clips[name] || (cur && clips[name] === cur.clip)) return
  prev = cur
  fade = 0
  cur = { clip: clips[name], t: 0, name }
}

// one-shot gesture: crossfade in, play once, crossfade back to the idle
let gestureReturn = null

function playGesture(name) {
  if (!clips[name] || gestureReturn) return
  gestureReturn = cur?.name ?? clipNames[0]
  playIdle(name)
}

const _qa = new THREE.Quaternion()
const _qb = new THREE.Quaternion()

function sampleInto(state, dt, weight) {
  const c = state.clip
  state.t = (state.t + dt) % c.duration
  const f = state.t * c.fps
  const f0 = Math.floor(f) % c.frames
  const f1 = (f0 + 1) % c.frames
  const a = f - Math.floor(f)
  for (const [name, tr] of Object.entries(c.bones)) {
    const bone = boneByKey[norm(name)]
    if (!bone) continue
    const p0 = tr.pos[f0], p1 = tr.pos[f1]
    const r0 = tr.rot[f0], r1 = tr.rot[f1]
    _qa.set(r0[0], r0[1], r0[2], r0[3])
    _qb.set(r1[0], r1[1], r1[2], r1[3])
    _qa.slerp(_qb, a)
    if (weight >= 1) {
      bone.position.set(p0[0] + (p1[0]-p0[0])*a, p0[1] + (p1[1]-p0[1])*a, p0[2] + (p1[2]-p0[2])*a)
      bone.quaternion.copy(_qa)
    } else {
      bone.position.lerp(new THREE.Vector3(p0[0] + (p1[0]-p0[0])*a, p0[1] + (p1[1]-p0[1])*a, p0[2] + (p1[2]-p0[2])*a), weight)
      bone.quaternion.slerp(_qa, weight)
    }
  }
}

function updateIdle(dt, t) {
  if (!cur) return false
  if (gestureReturn && cur.t + dt >= cur.clip.duration - Math.min(FADE_DUR, cur.clip.duration / 3)) {
    const back = gestureReturn
    gestureReturn = null
    playIdle(back)
  }
  if (t >= switchAt && clipNames.length > 1 && !gestureReturn) {
    const others = clipNames.filter(n => clips[n] !== cur.clip)
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

function blinkCurve(t) {  // 0..1 -> lid closure, fast close slow open
  return t < 0.4 ? t / 0.4 : 1 - (t - 0.4) / 0.6
}

let spin = false
const alive = true
controls.addEventListener('start', () => { spin = false })
const spinBtn = document.getElementById('spin')
spinBtn.textContent = 'rotate'
spinBtn.addEventListener('click', e => {
  spin = !spin
  e.target.textContent = spin ? 'pause' : 'rotate'
})

const look = { yaw: 0, pitch: 0 }
const timer = new THREE.Timer()
const _pq = new THREE.Quaternion()
const _target = new THREE.Quaternion()
const _e = new THREE.Euler()

window.viewer = { camera, controls, scene, renderer, setMorph, playIdle, playGesture,
  get head() { return head }, get clips() { return clips } }

renderer.setAnimationLoop(() => {
  resize()
  timer.update()
  const dt = Math.min(timer.getDelta(), 0.1)
  const t = timer.getElapsed()

  if (spin && model) model.rotation.y += 0.004

  if (alive && model) {
    updateMouth()
    const hasIdle = updateIdle(dt, t)
    // breathing fallback when no idle clip drives the spine
    if (!hasIdle && spineBone) {
      _e.set(Math.sin(t * 1.5) * 0.010, 0, 0)
      spineBone.quaternion.copy(spineBindQ).multiply(_target.setFromEuler(_e))
    }
    // look-at camera (eye contact with the viewer) layered over the head pose
    if (headBone && !spin) {
      const k = 1 - Math.exp(-dt * 6)
      headBone.getWorldPosition(_headPos)
      const dx = camera.position.x - _headPos.x
      const dy = camera.position.y - _headPos.y
      const dz = camera.position.z - _headPos.z
      const yawT = Math.atan2(dx, dz)
      const pitchT = -Math.atan2(dy, Math.hypot(dx, dz))
      look.yaw += (THREE.MathUtils.clamp(yawT, -0.6, 0.6) - look.yaw) * k
      look.pitch += (THREE.MathUtils.clamp(pitchT, -0.35, 0.35) - look.pitch) * k
      _e.set(look.pitch + Math.sin(t * 0.47) * 0.01,
             look.yaw + Math.sin(t * 0.31) * 0.015 + Math.sin(t * 0.73) * 0.01, 0)
      headBone.parent.getWorldQuaternion(_pq)
      const animWorld = _qa.copy(_pq).multiply(headBone.quaternion)
      _target.setFromEuler(_e).multiply(animWorld)
      headBone.quaternion.copy(_pq.invert().multiply(_target))
    }
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

  controls.update()
  renderer.render(scene, camera)
})

function resize() {
  const w = canvas.clientWidth, h = canvas.clientHeight
  if (canvas.width !== w * renderer.getPixelRatio() || canvas.height !== h * renderer.getPixelRatio()) {
    renderer.setSize(w, h, false)
    camera.aspect = w / h
    camera.updateProjectionMatrix()
  }
}

// ---- voice loop client (P2) ----
const talkBtn = document.getElementById('talk')
const captionEl = document.getElementById('caption')
const meterEl = document.getElementById('meter')
let ws = null
let recorder = null
let recChunks = []
let audioCtx = null
let analyser = null
let speaking = false

function connectWS() {
  if (location.protocol === 'file:') return
  ws = new WebSocket(`ws://${location.host}/ws`)
  ws.onmessage = e => {
    const msg = JSON.parse(e.data)
    if (msg.type === 'transcript') {
      if (speaking) { stopSpeaking(); talkBtn.classList.add('busy') }
      captionEl.innerHTML = `<span class="you">"${msg.text}"</span>`
    } else if (msg.type === 'speak') {
      onSpeak(msg)
    } else if (msg.type === 'speak_end') {
      endReceived = true
      if (!playingMsg && !speakQueue.length) finishSpeaking()
    } else if (msg.type === 'error') {
      captionEl.textContent = `(${msg.error})`
      talkBtn.classList.remove('busy')
    }
  }
  ws.onclose = () => setTimeout(connectWS, 2000)
}
connectWS()

function setMood(mood) {
  for (const k of MOOD_KEYS) setMorph(k, mood?.[k] ?? 0)
}

// sentence chunks queue up; each carries its own audio + rhubarb viseme track
const speakQueue = []
let playingMsg = null
let playStart = 0     // audioCtx.currentTime when the current chunk began
let curSrc = null     // live AudioBufferSourceNode, for barge-in
let endReceived = false  // server sent speak_end for the current turn

function stopSpeaking() {
  if (curSrc) { curSrc.onended = null; try { curSrc.stop() } catch { } curSrc = null }
  speakQueue.length = 0
  playingMsg = null
  speaking = false
  endReceived = false
  setMood(null)
  talkBtn.classList.remove('busy')
}

function finishSpeaking() {
  playingMsg = null
  speaking = false
  endReceived = false
  setMood(null)
  talkBtn.classList.remove('busy')
  setTimeout(() => { if (!speaking) captionEl.textContent = '' }, 4000)
}

function onSpeak(msg) {
  speakQueue.push(msg)
  if (!playingMsg) playNext()
}

function b64ToArrayBuffer(b64) {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return bytes.buffer
}

async function playNext() {
  const msg = speakQueue.shift()
  if (!msg) {
    if (endReceived) { finishSpeaking(); return }
    // queue starved mid-stream: mouth relaxes, stay busy until more arrives
    playingMsg = null
    speaking = false
    return
  }
  playingMsg = msg
  if (msg.seq === 0) {
    captionEl.innerHTML = `${captionEl.innerHTML.match(/<span[^>]*>.*?<\/span>/)?.[0] ?? ''}${msg.sentence}`
    if (msg.meter !== undefined) meterEl.textContent = `regard ${msg.meter}/100 · ${msg.tier}`
    setMood(msg.mood)
    if (msg.gesture === 'idle_switch' && clipNames.length > 1) {
      const others = clipNames.filter(n => clips[n] !== cur?.clip)
      playIdle(others[Math.floor(Math.random() * others.length)])
    } else if (msg.gesture && msg.gesture !== 'none' && clips['gesture_' + msg.gesture]) {
      playGesture('gesture_' + msg.gesture)
    }
  }
  // talking body language: a dialogue one-shot per chunk unless a gesture is playing
  if (!gestureReturn) {
    const angry = msg.emotion === 'annoyed' || (msg.mood?.MoodAnger ?? 0) > 0.2
    const pool = Object.keys(clips).filter(n =>
      angry ? n.startsWith('talk_angry') : (n.startsWith('talk_') && !n.startsWith('talk_angry')))
    if (pool.length && Math.random() < 0.8) playGesture(pool[Math.floor(Math.random() * pool.length)])
  } else {
    captionEl.innerHTML += ' ' + msg.sentence
  }
  audioCtx ??= new AudioContext()
  await audioCtx.resume()
  const buf = await audioCtx.decodeAudioData(b64ToArrayBuffer(msg.audio))
  const src = audioCtx.createBufferSource()
  src.buffer = buf
  analyser = audioCtx.createAnalyser()
  analyser.fftSize = 512
  src.connect(analyser)
  analyser.connect(audioCtx.destination)
  src.onended = playNext
  curSrc = src
  speaking = true
  playStart = audioCtx.currentTime
  src.start()
}

// mouth driver — called every rendered frame from the animation loop.
// Rhubarb cue track on the AudioContext clock; loudness jaw-flap as fallback.
const VISEME_LEAD = 0.06   // open the mouth slightly before the sound
const _fft = new Uint8Array(256)

function updateMouth() {
  if (!head) return
  if (!speaking) {
    for (const k of VISEME_KEYS) {
      const curV = head.morphTargetInfluences[head.morphTargetDictionary[k]] ?? 0
      if (curV > 0.001) setMorph(k, curV * 0.7)
    }
    return
  }
  const track = playingMsg?.visemes
  if (track && audioCtx) {
    const t = audioCtx.currentTime - playStart + VISEME_LEAD
    const cue = track.find(c => t >= c.s && t < c.e)
    for (const k of VISEME_KEYS) {
      const curV = head.morphTargetInfluences[head.morphTargetDictionary[k]] ?? 0
      const target = cue?.v === k ? (k === 'BigAah' ? 0.9 : 0.75) : 0
      const rate = target > curV ? 0.65 : 0.35   // fast attack, slower release
      setMorph(k, curV + (target - curV) * rate)
    }
  } else if (analyser) {
    analyser.getByteFrequencyData(_fft)
    let sum = 0
    for (let i = 2; i < 64; i++) sum += _fft[i]
    const level = Math.min(1, (sum / 62 / 255) * 3.2)
    const curV = head.morphTargetInfluences[head.morphTargetDictionary.BigAah] ?? 0
    setMorph('BigAah', curV + (level * 0.55 - curV) * 0.45)
  }
}

async function startRec() {
  if (!ws || ws.readyState !== 1) return
  // barge-in: talking over her (or over a pending reply) cancels the turn
  if (speaking || talkBtn.classList.contains('busy')) {
    ws.send(JSON.stringify({ type: 'interrupt' }))
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
    talkBtn.classList.add('busy')
    captionEl.textContent = '…'
    ws.send(JSON.stringify({ type: 'audio', data: b64 }))
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
window.addEventListener('keydown', e => { if (e.key === 't' && !e.repeat) startRec() })
window.addEventListener('keyup', e => { if (e.key === 't') stopRec() })

// text input path for testing without a mic
window.viewer.say = text => ws?.send(JSON.stringify({ type: 'text', text }))
