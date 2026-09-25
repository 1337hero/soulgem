// .anim clips (written by cmd/hkx-anim, see internal/anim/binary.go):
//   "SGA1" | u32 header length | JSON header | float32 payload
// A track stores one sample with stride 0 when it never moves, so sampling
// indexes every track as offset + frame * stride.
import * as THREE from 'three'

export type Track = { bone: string; pos: number; posStride: 0 | 3; rot: number; rotStride: 0 | 4 }
export type Clip = { duration: number; fps: number; frames: number; tracks: Track[]; data: Float32Array }
export type ClipState = { clip: Clip; t: number; name: string }
export type BoundTrack = Track & { target: THREE.Bone }

const MAGIC = 'SGA1'
const text = new TextDecoder()

export function parseClip(buffer: ArrayBuffer): Clip {
  const magic = text.decode(new Uint8Array(buffer, 0, 4))
  if (magic !== MAGIC) throw new Error(`not an .anim clip (magic ${JSON.stringify(magic)})`)
  const size = new DataView(buffer).getUint32(4, true)
  const header: Omit<Clip, 'data'> = JSON.parse(text.decode(new Uint8Array(buffer, 8, size)))
  return { ...header, data: new Float32Array(buffer, 8 + size) }
}

// Game bone names and GLB node names disagree on spaces and brackets.
export const boneKey = (name: string) => name.replace(/[^A-Za-z0-9]/g, '')

// Resolve a clip's tracks against one body's skeleton, dropping tracks for
// bones that body lacks. Done once per clip per body, never per frame.
export function bindClip(clip: Clip, bones: Record<string, THREE.Bone>): BoundTrack[] {
  return clip.tracks.flatMap(track => {
    const target = bones[boneKey(track.bone)]
    return target ? [{ ...track, target }] : []
  })
}

const _qa = new THREE.Quaternion()
const _qb = new THREE.Quaternion()
const _pos = new THREE.Vector3()

// Pose every bound bone at frame f0 + a toward f1, blended over the current
// pose by weight (1 = overwrite).
export function samplePose(tracks: BoundTrack[], data: Float32Array, f0: number, f1: number, a: number, weight: number) {
  for (const { target, pos, posStride, rot, rotStride } of tracks) {
    const p0 = pos + f0 * posStride, p1 = pos + f1 * posStride
    _pos.set(data[p0] + (data[p1] - data[p0]) * a,
             data[p0 + 1] + (data[p1 + 1] - data[p0 + 1]) * a,
             data[p0 + 2] + (data[p1 + 2] - data[p0 + 2]) * a)
    const r0 = rot + f0 * rotStride, r1 = rot + f1 * rotStride
    _qa.set(data[r0], data[r0 + 1], data[r0 + 2], data[r0 + 3])
    _qb.set(data[r1], data[r1 + 1], data[r1 + 2], data[r1 + 3])
    _qa.slerp(_qb, a)
    if (weight >= 1) {
      target.position.copy(_pos)
      target.quaternion.copy(_qa)
    } else {
      target.position.lerp(_pos, weight)
      target.quaternion.slerp(_qa, weight)
    }
  }
}
