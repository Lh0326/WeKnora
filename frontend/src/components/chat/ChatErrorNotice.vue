<template>
  <section class="chat-error-notice" role="alert">
    <strong>{{ presentation.title }}</strong>
    <p>{{ presentation.summary }}</p>
    <p v-if="failure.modelName || failure.providerName || failure.modelType" class="chat-error-context">
      <span v-if="failure.modelName">{{ presentation.labels.model }}: {{ failure.modelName }}</span>
      <span v-if="failure.providerName">{{ presentation.labels.provider }}: {{ failure.providerName }}</span>
      <span v-if="failure.modelType">{{ presentation.labels.stage }}: {{ failure.modelType }}</span>
    </p>
    <button v-if="canRetry" type="button" @click="emit('retry')">{{ presentation.labels.retry }}</button>
    <details>
      <summary>{{ presentation.labels.details }}</summary>
      <pre>{{ presentation.details }}</pre>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { presentChatError, type ChatFailure } from '@/utils/chatErrorPresentation'

const props = defineProps<{ failure: ChatFailure; canRetry?: boolean }>()
const emit = defineEmits<{ retry: [] }>()
const { locale } = useI18n()
const presentation = computed(() => presentChatError(props.failure, locale.value))
</script>

<style scoped>
.chat-error-notice { padding: 14px; border: 1px solid var(--td-warning-color-3, #eed7ad); border-radius: 8px; background: var(--td-bg-color-secondarycontainer, #faf8f3); font-size: 13px; line-height: 1.7; color: var(--td-text-color-primary); }
.chat-error-notice p { margin: 6px 0 10px; }
.chat-error-context { display: flex; flex-wrap: wrap; gap: 4px 12px; color: var(--td-text-color-secondary); overflow-wrap: anywhere; }
.chat-error-notice button { font: inherit; color: var(--td-brand-color); background: var(--td-bg-color-container); border: 1px solid var(--td-component-border); border-radius: 5px; padding: 3px 12px; margin-bottom: 8px; cursor: pointer; }
.chat-error-notice summary { cursor: pointer; color: var(--td-text-color-secondary); }
.chat-error-notice pre { margin: 8px 0 0; max-height: 240px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 11px; }
</style>
