export const LEARNING_UPDATED = 'weknora:learning-updated'
export const LEARNING_READ_STATUS = 'weknora:learning-read-status'
export function notifyReadStatus(kbId:string,slug:string,status:'saving'|'saved'|'error'|'disabled',tier:'normal'|'deep') {
 window.dispatchEvent(new CustomEvent(LEARNING_READ_STATUS,{detail:{kbId,slug,status,tier}}))
}
export function notifyLearningUpdated(kbId:string,slug:string) {
 window.dispatchEvent(new CustomEvent(LEARNING_UPDATED,{detail:{kbId,slug}}))
}
export const nodeStateLabels:Record<string,string>={unseen:'未开始',learning:'学习中',self_known:'自认已会',verified:'验证通过',review:'待巩固'}
export const nodeStateColors:Record<string,string>={unseen:'#c9ced5',learning:'#5c9dce',self_known:'#64b991',verified:'#148452',review:'#d59b35'}
