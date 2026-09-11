import type { LearningNodeView } from '@/api/learning/objectives'

export const learningStates = ['unseen', 'learning', 'self_known', 'verified', 'review'] as const
export type LearningState = typeof learningStates[number]
export function summarizeNodes(nodes: LearningNodeView[]) {
  const counts: Record<LearningState, number> = { unseen: 0, learning: 0, self_known: 0, verified: 0, review: 0 }
  for (const node of nodes) counts[node.state]++
  return { counts, total: nodes.length, covered: nodes.length - counts.unseen,
    known: counts.self_known + counts.verified, pending: counts.unseen + counts.learning + counts.review,
    verifiable: nodes.filter(n => n.objective_total > 0).length }
}

export function learningFocus(nodes: LearningNodeView[]): { state: LearningState; text: string; action: string } | null {
  const { counts, total } = summarizeNodes(nodes)
  if (!total) return null
  if (counts.review) return { state: 'review', text: `${counts.review} 个知识点需要巩固，先处理内容变化和已知薄弱点。`, action: '查看待巩固' }
  if (counts.unseen) return { state: 'unseen', text: `${counts.unseen} 个知识点尚未接触，可从下方路径或目标模块开始。`, action: '查看知识盲区' }
  if (counts.learning) return { state: 'learning', text: `已接触全部知识点，${counts.learning} 个尚未确认理解。会的可以直接点亮，拿不准的加入待巩固。`, action: '检查学习中的内容' }
  if (counts.self_known) return { state: 'self_known', text: '当前范围已标记学会。自认已会与验证通过分别保留，可随时将薄弱点加入待巩固。', action: '回顾自认已会' }
  return { state: 'verified', text: '当前知识点的已审核目标均已验证通过，可查看依据或选择新的知识库。', action: '查看验证记录' }
}

export function nodeEvidenceSummary(node: LearningNodeView): string {
  if (node.state === 'verified') return `当前内容的 ${node.objective_verified}/${node.objective_total} 个已审核目标通过验证。`
  if (node.state === 'self_known') return `来自你的主动确认，已移出默认待学；客观验证 ${node.objective_verified}/${node.objective_total} 个目标。`
  if (node.state === 'review') return node.updated ? '内容已更新，之前的学会确认需要重新核对。' : '已加入待巩固，请回看薄弱点；理解后可以重新确认学会。'
  if (node.reads > 0) return `已保存 ${node.reads} 条阅读记录${node.cites ? `、${node.cites} 条问答引用` : ''}；接触记录不代表已经理解。`
  if (node.cites > 0) return `问答曾引用此知识点 ${node.cites} 次，尚无阅读记录；可以先浏览内容再判断是否已会。`
  return '尚无阅读或确认记录。已熟悉的简单内容可以直接确认学会。'
}
export function groupLearningNodes(nodes: LearningNodeView[]) {
  const groups = new Map<string, { id: string; name: string; nodes: LearningNodeView[] }>()
  for (const node of nodes) {
    const key = node.folder_id || ''
    if (!groups.has(key)) groups.set(key, { id: key, name: node.folder_name || '未分组知识', nodes: [] })
    groups.get(key)!.nodes.push(node)
  }
  return [...groups.values()].map(g => ({ ...g, ...summarizeNodes(g.nodes) }))
    .sort((a, b) => b.counts.review - a.counts.review || b.pending - a.pending || a.name.localeCompare(b.name))
}
