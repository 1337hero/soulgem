import { expect, test } from 'bun:test'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { Store } from './store.ts'
import { SoulWork } from './soul.ts'

test('retiring a soul cancels its jobs and keeps its store open until they settle', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'soul-work-'))
  const oldStore = new Store(join(dir, 'old'), { embed: async () => null })
  const newStore = new Store(join(dir, 'new'), { embed: async () => null })
  const work = new SoulWork(oldStore)
  const pending = Promise.withResolvers<void>()
  let aborted = false
  try {
    const job = work.run(async controller => {
      await pending.promise
      aborted = controller.signal.aborted
      oldStore.setBulletin('Old soul briefing')
    })
    const retired = work.retire()
    expect(oldStore.getBulletin()).toBeNull()
    pending.resolve()
    await Promise.all([job, retired])
    expect(aborted).toBe(true)
    expect(newStore.getBulletin()).toBeNull()
    expect(() => oldStore.getBulletin()).toThrow()
    await expect(work.run(async () => {})).rejects.toThrow('retired')
  } finally {
    newStore.close()
    rmSync(dir, { recursive: true, force: true })
  }
})
