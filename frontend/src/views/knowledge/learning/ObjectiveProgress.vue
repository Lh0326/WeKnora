<template>
 <div class="obj-progress" aria-label="目标验证进度" aria-live="polite">
  <div class="verified"><strong>{{ summary.verified_objectives }}</strong><span>/ {{ summary.total_objectives }} 个目标已验证</span></div>
  <div v-if="summary.total_objectives > 0" class="progress-track" role="progressbar" aria-label="全库已审核目标的验证覆盖"
   :aria-valuenow="summary.verified_objectives" :aria-valuemax="summary.total_objectives" :aria-valuemin="0"
   :aria-valuetext="`${summary.verified_objectives} / ${summary.total_objectives} 个目标已验证`">
   <div class="progress-fill" :style="{width: percent + '%'}"></div>
  </div>
  <p v-else>暂无已审核的验证目标，可以先浏览材料。</p>
  <p>完成已审核的检查才计入；阅读不等于验证。</p>
  <p v-if="summary.goal_total_objectives !== summary.total_objectives">本次范围：{{ summary.goal_verified_objectives }} / {{ summary.goal_total_objectives }} 个目标已验证</p>
  <p v-if="summary.conflicting_objectives || summary.stale_objectives" class="attention">{{ summary.conflicting_objectives + summary.stale_objectives }} 个目标有冲突或内容变化，需复核。</p>
 </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { ObjectiveProgressSummary } from '@/api/learning/objectives'
const props = defineProps<{ summary: ObjectiveProgressSummary }>()
const percent = computed(() => props.summary.total_objectives > 0 ? Math.max(0, Math.min(100, props.summary.verified_objectives / props.summary.total_objectives * 100)) : 0)
</script>
<style scoped>
.obj-progress{display:flex;flex-direction:column;gap:8px}.verified{display:flex;align-items:baseline;gap:8px;flex-wrap:wrap}.verified strong{font-size:30px;font-weight:600;line-height:1.2}.verified span{font-size:13px;color:var(--td-text-color-secondary)}p{margin:0;font-size:12px;line-height:1.7;color:var(--td-text-color-secondary)}.attention{color:var(--td-warning-color,#9c6400)}
.progress-track{height:7px;overflow:hidden;background:var(--td-bg-color-component,#eef0f2);border-radius:8px;margin:4px 0}.progress-fill{height:100%;background:var(--td-brand-color,#07c05f);border-radius:inherit;transition:width .3s ease}
@media(prefers-reduced-motion:reduce){.progress-fill{transition:none}}
</style>
