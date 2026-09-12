export type ChatErrorKind = 'free_quota' | 'quota' | 'forbidden' | 'rate_limit' | 'other'

export interface ChatFailure {
  raw: string
  modelName?: string
  providerName?: string
  modelType?: 'chat' | 'embedding' | 'rerank'
}

const copy = {
  zh: {
    free_quota: ['免费额度已用完', '当前模型请求只允许使用免费额度，服务商已拒绝继续处理。请检查对应服务的额度和配置，恢复可用后再重试。'],
    quota: ['模型服务额度不足', '服务商报告额度不足。请检查对应服务的额度和配置，恢复可用后再重试。'],
    forbidden: ['请求没有访问权限', '服务返回了 403。请检查账号和模型访问权限，再重试。'],
    rate_limit: ['请求过于频繁', '服务暂时限制了请求频率，请稍后重试。'],
    other: ['本次回答未完成', '请求处理失败。你可以重试，或展开详情查看原因。'],
    details: '查看错误详情', retry: '重试', model: '模型', provider: '服务商', stage: '请求类型',
  },
  en: {
    free_quota: ['Free quota exhausted', 'This model request is restricted to free quota, and the provider declined it. Check the service quota and configuration, then retry when available.'],
    quota: ['Model service quota exhausted', 'The provider reported insufficient quota. Check the service quota and configuration, then retry when available.'],
    forbidden: ['Request not authorized', 'The service returned 403. Check account and model access permissions, then retry.'],
    rate_limit: ['Too many requests', 'The service is temporarily limiting requests. Please retry later.'],
    other: ['The answer could not be completed', 'The request failed. You can retry or expand the details to inspect the cause.'],
    details: 'View error details', retry: 'Retry', model: 'Model', provider: 'Provider', stage: 'Request type',
  },
  ru: {
    free_quota: ['Бесплатная квота исчерпана', 'Запрос ограничен бесплатной квотой, и провайдер отклонил его. Проверьте квоту и настройки сервиса, затем повторите запрос.'],
    quota: ['Недостаточно квоты сервиса', 'Провайдер сообщил о недостаточной квоте. Проверьте квоту и настройки, затем повторите запрос.'],
    forbidden: ['Нет разрешения на запрос', 'Сервис вернул 403. Проверьте права аккаунта и доступ к модели.'],
    rate_limit: ['Слишком много запросов', 'Сервис временно ограничивает частоту запросов. Повторите позже.'],
    other: ['Не удалось завершить ответ', 'Запрос завершился ошибкой. Повторите его или откройте подробности.'],
    details: 'Подробности ошибки', retry: 'Повторить', model: 'Модель', provider: 'Провайдер', stage: 'Тип запроса',
  },
  ko: {
    free_quota: ['무료 할당량 소진', '이 모델 요청은 무료 할당량만 사용하도록 제한되어 공급자가 거부했습니다. 서비스 할당량과 설정을 확인한 후 다시 시도하세요.'],
    quota: ['모델 서비스 할당량 부족', '공급자가 할당량 부족을 보고했습니다. 서비스 할당량과 설정을 확인한 후 다시 시도하세요.'],
    forbidden: ['요청 권한 없음', '서비스가 403을 반환했습니다. 계정 및 모델 접근 권한을 확인하세요.'],
    rate_limit: ['요청이 너무 많음', '서비스가 요청 빈도를 일시적으로 제한했습니다. 잠시 후 다시 시도하세요.'],
    other: ['답변을 완료하지 못했습니다', '요청에 실패했습니다. 다시 시도하거나 오류 세부 정보를 확인하세요.'],
    details: '오류 세부 정보', retry: '다시 시도', model: '모델', provider: '공급자', stage: '요청 유형',
  },
} as const

export function errorText(value: unknown): string {
  if (typeof value === 'string') return value
  if (value instanceof Error) return value.message
  try { return JSON.stringify(value, null, 2) || '' } catch { return String(value) }
}

export function classifyChatError(value: unknown): ChatErrorKind {
  const raw = errorText(value)
  // An explicit provider quota code outranks its HTTP 403 envelope. Ordinary
  // access-denied responses must not be mislabeled as billing/quota failures.
  if (/\bAllocationQuota\.FreeTierOnly\b/i.test(raw)) return 'free_quota'
  if (/\binsufficient_quota\b|\b(?:free\s+)?quota\s+(?:is\s+)?(?:exhausted|exceeded)\b/i.test(raw)) return 'quota'
  if (/\b(?:HTTP|status(?:\s+code)?|status_code)\s*[:=]?\s*["']?403\b|["']status["']\s*:\s*403\b/i.test(raw)) return 'forbidden'
  if (/\b(?:HTTP|status(?:\s+code)?|status_code)\s*[:=]?\s*["']?429\b|\brate_limit_exceeded\b/i.test(raw)) return 'rate_limit'
  return 'other'
}

export function createChatFailure(raw: unknown, payload?: Record<string, unknown>): ChatFailure {
  const failure: ChatFailure = { raw: errorText(raw) }
  // A requested chat model is not necessarily the model that failed (retrieval
  // can use embedding/rerank). Only explicit error context may identify it.
  const context = payload?.error_context
  if (context && typeof context === 'object') {
    const meta = context as Record<string, unknown>
    if (typeof meta.model_name === 'string' && meta.model_name.trim()) failure.modelName = meta.model_name
    if (typeof meta.provider === 'string' && meta.provider.trim()) failure.providerName = meta.provider
    if (meta.model_type === 'chat' || meta.model_type === 'embedding' || meta.model_type === 'rerank') failure.modelType = meta.model_type
  }
  return failure
}

/** Preserve diagnostics while hiding credentials if an upstream error echoed them. */
export function redactChatErrorDetails(raw: string): string {
  return raw
    .replace(/((?:authorization|api[_-]?key|access[_-]?token|password)["']?\s*[:=]\s*["']?)(?:Bearer\s+)?[^\s,"'\r\n}]+/gi, '$1[redacted]')
    .replace(/\bBearer\s+[^\s,"'\r\n}]+/gi, 'Bearer [redacted]')
}

export function presentChatError(failure: ChatFailure, locale = 'zh-CN') {
  const language = locale.toLowerCase().split('-')[0] as keyof typeof copy
  const labels = copy[language] || copy.en
  const kind = classifyChatError(failure.raw)
  const [title, summary] = labels[kind]
  return { kind, title, summary, details: redactChatErrorDetails(failure.raw), labels }
}

export function getMessageChatFailure(message: Record<string, unknown> | undefined): ChatFailure | null {
  if (!message) return null
  const recorded = message.chatError
  if (recorded && typeof recorded === 'object' && typeof (recorded as ChatFailure).raw === 'string') return recorded as ChatFailure
  // Old persisted failures have no error flag. Match the actual service error
  // envelope at the start, not an answer that happens to discuss a quota code.
  const content = typeof message.content === 'string' ? message.content.trim() : ''
  if (message.role === 'assistant' && message.is_completed && /^(?:API request failed with status \d{3}:|(?:aliyun|zhipu) rerank API error:|error, status code: \d{3},)/i.test(content)) {
    return createChatFailure(content)
  }
  return null
}
