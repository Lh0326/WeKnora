import type {EstimateLevel,LearningNodeView} from '@/api/learning/objectives'

export const estimateLevels:EstimateLevel[]=['unseen','introduced','developing','self_reported','familiar','review']
export const estimateLabels:Record<EstimateLevel,string>={unseen:'未接触',introduced:'初步接触',developing:'已阅读',self_reported:'自认熟悉',familiar:'检查支持',review:'建议巩固'}
export const estimateColors:Record<EstimateLevel,string>={unseen:'#a7afb8',introduced:'#9bbfc6',developing:'#59aa9a',self_reported:'#77ab77',familiar:'#168357',review:'#b58124'}
export function estimateLevel(node:LearningNodeView):EstimateLevel{
 return node.estimate?.level||({unseen:'unseen',learning:'introduced',self_known:'self_reported',verified:'familiar',review:'review'} as const)[node.state]
}
export function summarizeEstimates(nodes:LearningNodeView[]){
 const counts:Record<EstimateLevel,number>={unseen:0,introduced:0,developing:0,self_reported:0,familiar:0,review:0}
 for(const n of nodes)counts[estimateLevel(n)]++
 return {total:nodes.length,counts,covered:nodes.length-counts.unseen,verified:nodes.filter(n=>n.state==='verified').length,
  uncertain:nodes.filter(n=>n.estimate&&n.estimate.answers===0).length}
}
export function estimateExplanation(node:LearningNodeView):string{
 const e=node.estimate
 if(!e)return '模型估计暂不可用，保留已保存的学习记录。'
 if(e.basis==='prior'&&!e.corrections)return '尚无当前内容的学习记录，推荐从阅读开始。'
 const basis=e.answers?`已有 ${e.answers} 个独立题族提供检查证据`
  :e.coverage>0?'已有阅读进度；尚无独立检查，不显示能力数值'
  :e.memory?'已有回忆记录，理解程度仍缺少独立作答依据'
  :e.corrections?'已保存你的熟悉或困难反馈，仅用于调整学习顺序'
  :'仅有问答触及记录，尚不能据此判断理解程度'
 const parts=[basis]
 if(e.corrections)parts.push(`保留 ${e.corrections} 次熟悉或困难反馈`)
 if(e.memory)parts.push(`FSRS 已参考 ${e.memory.observations} 次回忆，预计当前保持率 ${Math.round(e.memory.retrievability*100)}%`)
 return `${parts.join('；')}。阅读、自评与检查事实分别保留。`
}
