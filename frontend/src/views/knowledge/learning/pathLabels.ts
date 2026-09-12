export function pathActionLabel(action:string):string {
 return ({overview:'了解全貌',read:'阅读理解',bridge:'补齐先修',practice:'练习巩固',verify:'验证掌握',review:'复习检查',confirm:'确认理解',recall:'间隔回忆'} as Record<string,string>)[action] || '学习'
}
export function pathReasonLabel(code?:string):string {
	if(code==='plan_reason_reading_gap')return '结合阅读覆盖、相关性与所需时间安排下一项。'
	if(code==='plan_reason_model_check')return '用一次可选检查补充这个目标的独立证据。'
 if(code==='plan_reason_user_review')return '当前估计或你的反馈提示这里需要巩固，优先回看。'
 if(code==='plan_reason_scheduled_recall')return '你主动加入的复习已到期，尝试回忆并反馈难度。'
 if(code==='plan_reason_review_due')return '当前目标需要复核，请检查是否仍能独立应用。'
 if(code==='plan_reason_memory_topic')return '与你的长期记忆主题相关，建议优先了解。'
 if(code==='plan_reason_frequent_document')return '来自你最近使用的常用资料，贴近实际查阅需求。'
 return ({plan_reason_first_contact:'这页尚未完成阅读，先了解内容和适用条件。',plan_reason_practice_before_verify:'先练习关键步骤，再检查掌握情况。',plan_reason_verify_contract:'已读过相关材料，可以用一次检查验证理解。',plan_reason_bridge_prereq:'这是后续内容的前置知识，先补齐会更顺畅。',plan_reason_bridge_after_failures:'上次检查遇到困难，先回看前置材料。',plan_reason_goal_successor:'沿着目标的先修关系继续深入。',plan_reason_continue_thread:'接着最近学习过的内容继续。',plan_reason_fast_track_challenge:'根据你的已有基础，直接检查这个目标。',plan_reason_docorder_fallback:'按来源位置安排阅读，不据此判断知识先修关系。',plan_reason_user_review:'你标记了需要再学，优先补齐这个知识点。',plan_reason_continue_module:'接着最近学习的模块，减少在不同主题间切换。',plan_reason_confirm_understanding:'已有相关接触记录；若已理解，可直接确认学会并学习下一项。'} as Record<string,string>)[code||''] || '结合当前学习记录与材料顺序推荐。'
}

export function pathPersonalizationLabel(status?:string):string {
 return ({available:'已找到与你的长期记忆或近 90 天常用资料关联的知识点。',disabled:'本轮仅依据学习记录、目标与材料顺序。',unavailable:'长期记忆暂不可用，本轮仍按学习记录与材料顺序规划。',no_signals:'尚无匹配的长期记忆或常用资料，先按当前学习记录建立路径。'} as Record<string,string>)[status||'']||'可优先考虑与你长期关注的主题相关的内容。'
}
export function pathDegradeLabel(code:string):string {
 if(code==='plan_degrade_explored')return '当前材料已有阅读记录，可以回顾薄弱点或选择其他模块。'
 if(code==='plan_degrade_no_verification')return '当前没有新的已审核验证题目，仍可通过阅读推进学习。'
 if(code==='plan_degrade_covered')return '当前范围暂没有待安排的新学习项；可查看学习记录或选择其他模块。'
 return ({plan_degrade_complete:'本次目标已通过验证，可选择新模块或回顾。',plan_degrade_explored:'当前材料已读过，可确认已会或选择其他模块。',plan_degrade_no_verification:'当前没有新的已审核验证题目，仍可阅读并确认理解。',plan_degrade_cycle:'部分先修关系存在循环，当前可先自由阅读，等待关系修正。',plan_degrade_no_content:'本库还没有可学习的实体或概念，请先生成 Wiki。',plan_degrade_orientation:'当前先按材料顺序建立知识全貌。',plan_degrade_covered:'当前范围均已掌握或本轮已跳过，可选择其他模块。',plan_degrade_budget:'当前范围暂未找到可执行步骤，可选择其他模块或从星图回看内容。'} as Record<string,string>)[code] || '当前路径需要调整，请更换学习范围后重试。'
}
