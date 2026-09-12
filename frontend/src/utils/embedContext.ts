/** Prefix host-injected context onto the user query for embed chat. */
export function buildQueryWithHostContext(
  query: string,
  hostContext?: Record<string, unknown>,
): string {
  if (!hostContext || !Object.keys(hostContext).length) return query
  const lines = Object.entries(hostContext)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${k}: ${typeof v === 'string' && !v.includes('\n') ? v : JSON.stringify(v)}`)
  if (!lines.length) return query
  return `[Host context]\n${lines.join('\n')}\n[/Host context]\n\n${query}`
}

/** Keep private host metadata out of message bubbles, including old sessions. */
export function visibleUserQuery(content: unknown): string {
  if (typeof content !== 'string') return ''
  if (!content.startsWith('[Host context]\n')) return content
  const end = content.indexOf('\n[/Host context]\n\n')
  if (end >= 0) return content.slice(end + '\n[/Host context]\n\n'.length)
  const legacyEnd = content.indexOf('\n\n')
  if (legacyEnd >= 0 && /\n(?:scene|current_page_slug|kb_progress):/.test(content.slice(0, legacyEnd))) return content.slice(legacyEnd + 2)
  return content
}
