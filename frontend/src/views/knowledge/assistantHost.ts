import { ref } from 'vue'

/**
 * Module-scoped host context for the KB knowledge assistant widget.
 *
 * The widget mounts ONCE at the KnowledgeBase root so it stays in the
 * bottom-right corner on every tab (documents / wiki / graph / learning /
 * …) instead of only the wiki view. Whichever tab is active publishes its
 * context into this store, so the assistant — and the widget header's
 * "当前界面" line — always reflects where the user actually is:
 *
 *   - KnowledgeBase's tab watcher publishes the generic scene for
 *     non-wiki tabs (learning / documents / …);
 *   - WikiBrowser (mounted for wiki + graph tabs) publishes the richer
 *     wiki-page context (page title / slug / type) and falls back to the
 *     generic wiki scene when no page is open.
 */
export const assistantHostContext = ref<Record<string, string> | null>(null)

export function setAssistantHost(ctx: Record<string, string> | null): void {
  assistantHostContext.value = ctx
}
