/** Fence callbacks across navigation, including leaving and returning to the same scope. */
export function createRequestScope(readScope: () => string) {
  let epoch = 0, disposed = false
  return {
    capture() {
      const started = epoch, scope = readScope()
      return () => !disposed && started === epoch && scope === readScope()
    },
    invalidate() { epoch++ },
    dispose() { disposed = true; epoch++ },
  }
}
