import { inject, shallowReadonly, shallowRef, type InjectionKey } from 'vue'

/** Each KB screen owns its current page. Cached or detached readers cannot
 * overwrite the foreground tab or publish a previous account's content. */
export function createAssistantHost() {
  const context = shallowRef<Record<string, string> | null>(null)
  let identity = '', scene = ''
  function setScope(nextIdentity: string) {
    if (identity === nextIdentity) return
    identity = nextIdentity
    context.value = scene ? { scene } : null
  }
  function setScene(nextScene: string) {
    scene = nextScene
    context.value = { scene }
  }
  function publisher(scenes: string[]) {
    const owner = identity
    return (value: Record<string, string>) => {
      if (owner === identity && scenes.includes(scene)) context.value = { ...value }
    }
  }
  function clear() { identity = ''; scene = ''; context.value = null }
  return { context: shallowReadonly(context), setScope, setScene, publisher, clear }
}

export const assistantHostKey: InjectionKey<ReturnType<typeof createAssistantHost>> = Symbol('assistant-host')

export function useAssistantHostPublisher(scenes: string[]) {
  const host = inject(assistantHostKey, null)
  return host?.publisher(scenes) ?? (() => {})
}
