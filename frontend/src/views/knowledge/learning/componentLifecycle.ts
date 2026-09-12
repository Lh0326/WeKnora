/** A committed action belongs to a KB scope even after its reader closes. */
export async function completeComponentAction<T extends {recorded: boolean}>(
  submit: () => Promise<T>,
  hooks: {
    scopeCurrent: () => boolean
    readerCurrent: () => boolean
    applyToReader: (result: T) => void
    refresh: () => Promise<void>
    changed: () => void
  },
): Promise<T | undefined> {
  const result = await submit()
  if (!hooks.scopeCurrent()) return
  if (hooks.readerCurrent()) hooks.applyToReader(result)
  if (result.recorded) {
    await hooks.refresh()
    if (hooks.scopeCurrent()) hooks.changed()
  }
  if (hooks.scopeCurrent() && hooks.readerCurrent()) return result
}

/** Pausing discards unfinished exposure; resuming starts a full reading interval. */
export function createComponentReadingClock() {
  let armed = false, elapsed = 0, required = 0, last = 0
  return {
    start(seconds: number, now: number) {
      armed = true
      elapsed = 0
      required = Math.max(0, seconds * 1000)
      last = now
    },
    pause() { armed = false; elapsed = 0 },
    tick(now: number, canRead: boolean): boolean {
      const delta = Math.max(0, Math.min(1500, now - last))
      last = now
      if (!armed || !canRead) return false
      elapsed += delta
      if (elapsed < required) return false
      armed = false
      return true
    },
  }
}
