import type { ObjectiveViewResponse } from '@/api/learning/objectives'
import {nodeStateLabels,nodeStateColors} from './learningEvents'
import {estimateLabels,estimateColors,estimateExplanation} from './learningEstimates'
import type { ConstellationNode } from './constellationLayout'

/** Display projection only. Activity, self-report and legacy scores never certify a page. */
export function projectObjectiveNodes(nodes: ConstellationNode[], view: ObjectiveViewResponse | null): ConstellationNode[] {
 const bySlug = new Map<string, ObjectiveViewResponse['entries']>()
 for (const entry of view?.entries ?? []) {
  const group = bySlug.get(entry.slug) ?? []
  group.push(entry); bySlug.set(entry.slug, group)
 }
 const workflow = new Map((view?.nodes||[]).map(n=>[n.slug,n]))
 return nodes.map(node => {
  const fact = workflow.get(node.slug)
  if(fact?.estimate){const e=fact.estimate;return {...node,level:e.level==='familiar'?'familiar':e.level==='unseen'?'unseen':'touched',p_eff:undefined,skipped:false,self_assess:undefined,low_confidence:false,last_evidence_at:undefined,last_activity_at:undefined,evidence_count:fact.reads+fact.cites,verification_complete:fact.state==='verified',learning_contacted:e.level!=='unseen',verification_label:`${estimateLabels[e.level]} · ${estimateExplanation(fact)}${fact.state==='verified'?' · 目标验证通过':''}`,verification_color:estimateColors[e.level]}}
  if(fact){return {...node,level:fact.state==='verified'?'mastered':fact.state==='self_known'?'familiar':fact.state==='unseen'?'unseen':'touched',p_eff:undefined,skipped:false,self_assess:undefined,low_confidence:false,last_evidence_at:undefined,last_activity_at:undefined,evidence_count:fact.reads+fact.cites,verification_complete:fact.state==='verified',learning_contacted:fact.state!=='unseen',verification_label:`${nodeStateLabels[fact.state]} · 阅读 ${fact.reads} 次${fact.updated?' · 内容已更新':''}${fact.objective_total?` · ${fact.objective_verified}/${fact.objective_total} 个目标验证通过`:''}`,verification_color:nodeStateColors[fact.state]}}

  const entries = bySlug.get(node.slug) ?? []
  const published = entries.filter(e => e.objective_status === 'published')
  const verified = published.filter(e => e.state === 'verified').length
  const allVerified = published.length > 0 && verified === published.length
  const needsReview = published.some(e => e.state === 'conflicting' || e.state === 'stale_content')
  const partial = verified > 0 || published.some(e => e.state === 'partial')
  const contacted = entries.some(e => e.exposure.reads > 0 || e.exposure.cites > 0)
  const available = view !== null && view.projection_version !== 'unavailable'
  const label = !available ? '验证状态暂不可用' : !published.length ? '暂无已审核目标' :
   `${verified}/${published.length} 个目标已验证${needsReview ? '，需复核' : partial && !allVerified ? '，仍有目标未验证' : ''}`
  return {...node, level: allVerified ? 'mastered' : partial ? 'familiar' : contacted ? 'touched' : 'unseen',
   p_eff: undefined, skipped: false, self_assess: undefined, low_confidence: false,
   last_activity_at: undefined, last_evidence_at: undefined,
   evidence_count: published.reduce((n,e) => n + e.evidence.eligible_passes + e.evidence.eligible_failures, 0),
   verification_complete: allVerified, verification_label: label,
   verification_color: needsReview ? '#b58124' : allVerified ? '#038626' : partial ? '#69ac87' : '#a7afb8'}
 })
}
