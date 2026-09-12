import type {ComponentView} from '@/api/learning/components'
import type {ConstellationNode,ConstellationEdge,ZoneAxis} from './constellationLayout'
export const componentLabels:Record<string,string>={unseen:'未接触',touched:'初步接触',learning:'已阅读',self_reported:'自评熟悉',familiar:'检查支持',review:'建议巩固'}
export const componentColors:Record<string,string>={unseen:'#a7afb8',touched:'#9bbfc6',learning:'#59aa9a',self_reported:'#568b78',familiar:'#168357',review:'#b58124'}
export const componentActionLabels:Record<string,string>={read:'学习目标',check:'情境检查',recall:'回忆复习'}
export function componentGraph(view:ComponentView|null):{nodes:ConstellationNode[];edges:ConstellationEdge[];zones:ZoneAxis[]}{
 if(!view)return {nodes:[],edges:[],zones:[]}
 const ids=new Map(view.components.map(c=>[c.material.key,`kc:${c.id}`]))
 const nodes:ConstellationNode[]=view.components.map(c=>({slug:`kc:${c.id}`,title:c.material.title,level:({unseen:'unseen',touched:'touched',learning:'touched',self_reported:'touched',familiar:'familiar',review:'touched'} as Record<string,string>)[c.state.level]||'unseen',folder_id:c.material.topic,folder_name:c.material.topic,p_eff:c.state.performance_observed?c.state.familiarity:undefined,evidence_count:c.state.checks,last_activity_at:c.state.last_study_at,learning_contacted:c.state.level!=='unseen',verification_complete:false,verification_color:c.available?componentColors[c.state.level]:'#a7afb8',verification_label:c.available?`${componentLabels[c.state.level]} · ${c.state.basis} · 适用条件：${c.material.condition}`:c.source_problem,low_confidence:c.state.checks<2}))
 const edges:ConstellationEdge[]=[];const seen=new Set<string>()
 for(const c of view.components){for(const [kind,keys] of [['prerequisite',c.material.prerequisites||[]],['wikilink',c.material.related||[]]] as const){for(const key of keys){const from=ids.get(key),to=`kc:${c.id}`;if(!from)continue;const identity=kind==='wikilink'?[from,to].sort().join('|'):`${from}>${to}`;if(seen.has(identity))continue;seen.add(identity);edges.push({from,to,kind})}}}
 for(const node of nodes){const c=view.components.find(c=>`kc:${c.id}`===node.slug);if(!c?.available){node.level='unseen';node.learning_contacted=false}}
 const zones:ZoneAxis[]=[...new Set(view.components.map(c=>c.material.topic))].map(topic=>({id:topic,name:topic,next:view.steps.find(s=>view.components.some(c=>c.id===s.id&&c.material.topic===topic))?.id})).map(z=>({...z,next:z.next?`kc:${z.next}`:null}))
 return {nodes,edges,zones}
}
