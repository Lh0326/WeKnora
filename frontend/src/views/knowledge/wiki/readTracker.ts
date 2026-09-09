/**
 * Tiered wiki-read tracker for the learning signal.
 *
 * Replaces the fire-on-open recordWikiRead calls with dwell-aware tiers:
 * - normal: the page stayed open ≥ NORMAL_DELAY_MS (5s). Quick flips
 *   (< 5s) record nothing — a glance is not a study action.
 * - deep: the page stayed open ≥ DEEP_THRESHOLD_MS (60s) when left.
 *   Fires once per open-session on leave (backend caps at one per node
 *   per 48h window).
 *
 * One tracker instance tracks ONE "current page" slot: callers call
 * enter(slug) whenever the active page changes (browser selection, graph
 * drawer, drawer close revert) and leave()/settle on unmount. Tab-hidden
 * settles the current page (deep check) and re-enters on visible, so
 * hidden time never counts as dwell.
 */

export const READ_NORMAL_DELAY_MS = 5_000
export const READ_DEEP_THRESHOLD_MS = 60_000

export interface ReadRecorder {
  (slug: string, tier: 'normal' | 'deep'): void
}

export function createWikiReadTracker(record: ReadRecorder) {
  let currentSlug = ''
  let openedAt: number | null = null
  let started = false
  let normalTimer: ReturnType<typeof setTimeout> | null = null

  function pause() {
    if (normalTimer) {
      clearTimeout(normalTimer)
      normalTimer = null
    }
    const dwell = openedAt === null ? 0 : Date.now() - openedAt
    if (currentSlug && dwell >= READ_DEEP_THRESHOLD_MS) {
      record(currentSlug, 'deep')
    }
    openedAt = null
  }

  function settle() {
    pause()
    currentSlug = ''
  }

  function resume() {
    if (!currentSlug || document.hidden || openedAt !== null) return
    const slug = currentSlug
    openedAt = Date.now()
    normalTimer = setTimeout(() => {
      normalTimer = null
      if (!document.hidden && currentSlug === slug) record(slug, 'normal')
    }, READ_NORMAL_DELAY_MS)
  }

  function enter(slug: string) {
    if (slug === currentSlug) return
    settle()
    if (!slug) return
    currentSlug = slug
    resume()
  }

  function onVisibilityChange() {
    if (document.hidden) {
      pause()
    } else {
      resume()
    }
  }

  function start() {
    if (started) return
    started = true
    document.addEventListener('visibilitychange', onVisibilityChange)
  }

  function stop() {
    started = false
    document.removeEventListener('visibilitychange', onVisibilityChange)
    settle()
  }

  return { enter, settle, start, stop }
}
