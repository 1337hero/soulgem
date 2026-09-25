import { expect, test } from 'bun:test'
import * as THREE from 'three'
import { bindClip, parseClip, samplePose } from './anim.ts'

// Mirrors internal/anim/binary.go: magic, u32 header length, padded JSON, float32s.
function encode(header: object, payload: number[]) {
  let json = JSON.stringify(header)
  while ((8 + json.length) % 4) json += ' '
  const bytes = new Uint8Array(8 + json.length + payload.length * 4)
  bytes.set(new TextEncoder().encode('SGA1'))
  new DataView(bytes.buffer).setUint32(4, json.length, true)
  bytes.set(new TextEncoder().encode(json), 8)
  new Float32Array(bytes.buffer, 8 + json.length).set(payload)
  return bytes.buffer
}

// Root slides 0 -> 10 on x over two frames; its rotation never changes, so it
// is stored once with stride 0.
const clip = parseClip(encode({
  duration: 2 / 30, fps: 30, frames: 2,
  tracks: [
    { bone: 'NPC Root [Root]', pos: 0, posStride: 3, rot: 6, rotStride: 0 },
    { bone: 'Not In This Body', pos: 0, posStride: 0, rot: 6, rotStride: 0 },
  ],
}, [0, 0, 0, 10, 0, 0, 0, 0, 0, 1]))

function body() {
  const root = new THREE.Bone()
  return { root, tracks: bindClip(clip, { NPCRootRoot: root }) }
}

test('bone names bind by normalized key and unknown bones drop out', () => {
  const { root, tracks } = body()
  expect(tracks.map(t => t.target)).toEqual([root])
})

test('moving tracks interpolate between frames and constant tracks hold', () => {
  const { root, tracks } = body()
  root.quaternion.set(0.5, 0.5, 0.5, 0.5)
  samplePose(tracks, clip.data, 0, 1, 0.25, 1)
  expect(root.position.toArray()).toEqual([2.5, 0, 0])
  expect(root.quaternion.toArray()).toEqual([0, 0, 0, 1])
})

test('a partial weight blends over the existing pose', () => {
  const { root, tracks } = body()
  root.position.set(4, 0, 0)
  samplePose(tracks, clip.data, 1, 0, 0, 0.5)
  expect(root.position.toArray()).toEqual([7, 0, 0])
})

test('a file without the magic is rejected', () => {
  expect(() => parseClip(new TextEncoder().encode('{"not":"anim"}').buffer)).toThrow(/not an .anim clip/)
})
