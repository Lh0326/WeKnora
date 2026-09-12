import type {LearningComponent} from '@/api/learning/components'

export function componentModules(components: LearningComponent[]) {
 const counts = new Map<string, number>()
 for (const c of components) counts.set(c.material.topic, (counts.get(c.material.topic) || 0) + 1)
 return [...counts].map(([topic,count])=>({topic,count})).sort((a,b)=>a.topic.localeCompare(b.topic,'zh-CN'))
}

// These links come only from the material's explicit prerequisites. A shared
// topic or a related edge is not evidence that one skill must precede another.
export function componentLearningLinks(components: LearningComponent[], id: string) {
 const active=components.find(c=>c.id===id)
 if(!active)return {before:[],after:[],missing:0}
 const keys=new Map(components.map(c=>[c.material.key,c]))
 const prerequisiteKeys=[...new Set(active.material.prerequisites||[])]
 return {
  before:prerequisiteKeys.map(key=>keys.get(key)).filter((c):c is LearningComponent=>!!c),
  after:components.filter(c=>c.id!==id&&(c.material.prerequisites||[]).includes(active.material.key)),
  missing:prerequisiteKeys.filter(key=>!keys.has(key)).length,
 }
}
