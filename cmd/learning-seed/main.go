// learning-seed imports demo drafts only. A real content owner must review each definition and task in the authenticated application.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Tencent/WeKnora/internal/application/repository"
	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"os"
)

func main() {
	dsn := flag.String("dsn", "", "Postgres DSN; prefer WEKNORA_SEED_DSN environment variable")
	kb := flag.String("kb", "", "knowledge base id (required)")
	tenant := flag.Uint64("tenant", 0, "tenant id (required)")
	flag.Parse()
	if *dsn == "" {
		*dsn = os.Getenv("WEKNORA_SEED_DSN")
	}
	if *dsn == "" || *kb == "" || *tenant == 0 {
		fmt.Fprintln(os.Stderr, "learning-seed: DSN, -kb and -tenant required")
		os.Exit(2)
	}
	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	must(err)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, *tenant)
	must(db.Transaction(func(tx *gorm.DB) error {
		repo := repository.NewLearningRepository(tx)
		svc := learning.NewService(repo, repository.NewWikiPageRepository(tx), repository.NewChunkRepository(tx), nil, nil, nil)
		objectives, items, tasks := demoObjectives(*tenant, *kb), demoItems(*tenant, *kb), demoTasks(*tenant, *kb)
		// Namespaced deterministic IDs let separate KBs use the draft pack without cross-scope collisions.
		prefix := fmt.Sprintf("seed:%d:%s:", *tenant, *kb)
		seedID := func(id string) string { return uuid.NewSHA1(uuid.NameSpaceOID, []byte(prefix+id)).String() }
		for i := range objectives {
			objectives[i].ID = seedID(objectives[i].ID)
		}
		for i := range items {
			items[i].ID = seedID(items[i].ID)
			items[i].ObjectiveID = seedID(items[i].ObjectiveID)
		}
		for i := range tasks {
			tasks[i].ID = seedID(tasks[i].ID)
			tasks[i].ObjectiveID = seedID(tasks[i].ObjectiveID)
		}
		for _, o := range objectives {
			var count int64
			if e := tx.Model(&types.LearningObjective{}).Where("id = ?", o.ID).Count(&count).Error; e != nil {
				return e
			}
			if count > 0 {
				return fmt.Errorf("draft pack already exists; refusing to overwrite reviewed content: %s", o.ID)
			}
		}
		for i := range objectives {
			if e := repo.UpsertObjective(ctx, &objectives[i]); e != nil {
				return e
			}
		}
		for i := range items {
			_, _, hash, e := learning.SeedQuizEvidence(ctx, svc, *tenant, *kb, items[i].Slug)
			if e != nil {
				return e
			}
			if hash == "" {
				return fmt.Errorf("missing source for %s", items[i].Slug)
			}
			items[i].EvidenceHash = hash
			if e = repo.UpsertQuizItem(ctx, &items[i]); e != nil {
				return e
			}
		}
		for i := range tasks {
			if e := repo.UpsertTask(ctx, &tasks[i]); e != nil {
				return e
			}
		}
		return nil
	}))
	fmt.Println("Draft content imported. Published=0; human review remains pending. This is not user-trial evidence.")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "learning-seed:", err)
		os.Exit(1)
	}
}

// Draft content pack adapted from the previous sample. References are candidate sources,
// not a claim that a human has checked the revised wording or every new constraint.

func demoObjectives(tenant uint64, kb string) []types.LearningObjective {
	v1 := "v1"
	mk := func(id, slug, title, behavior string, contract string) types.LearningObjective {
		return types.LearningObjective{
			ID: id, TenantID: tenant, KnowledgeBaseID: kb, Slug: slug,
			Title: title, Behavior: behavior, CapabilityType: func() string {
				if contract == types.ObjectiveContractTaskChecks {
					return "operation"
				}
				return "concept"
			}(),
			ContractType: contract, ContractVersion: types.ObjectiveContractVersion,
			ContentVersion: v1, Status: types.LearningObjectiveStatusDraft,
		}
	}
	return []types.LearningObjective{
		mk("obj-llm-init", "concept/da-yu-yan-mo-xing", "说明 LLM 初始化参数", "能说明 ChatTongyi 初始化中 model 与 temperature 参数的作用", types.ObjectiveContractConceptTwoFamily),
		mk("obj-temp-direction", "concept/wen-du-can-shu", "温度参数方向", "能判断 temperature 升高/降低对输出随机性的影响方向", types.ObjectiveContractConceptTwoFamily),
		mk("obj-lc-role", "entity/langchain", "LangChain 职责", "能识别 LangChain 在示例项目中的职责边界（模板/工具/Agent，不含训练）", types.ObjectiveContractConceptTwoFamily),
		mk("obj-tool-flow", "concept/gong-ju-diao-yong", "工具调用差异", "能区分普通模型调用与工具调用流程的关键差异", types.ObjectiveContractConceptTwoFamily),
		mk("obj-tools-three", "entity/langchain-tools-py", "函数工具的识别", "能识别 @tool 函数工具，并指出模型识别其用途所依赖的函数描述信息", types.ObjectiveContractConceptTwoFamily),
		mk("obj-sql-position", "entity/sqlite", "SQLite 定位", "能说明 SQLite 在知识库升级方向中的定位与数据库类型", types.ObjectiveContractConceptTwoFamily),
		mk("obj-rag-upgrade", "concept/jian-suo-zeng-qiang-sheng-cheng", "知识库升级方案", "能根据持久化、检索相关证据和回答溯源的约束，选择知识库升级方案（配置选择任务，不等于代码部署能力）", types.ObjectiveContractTaskChecks),
	}
}

func demoItems(tenant uint64, kb string) []types.LearningQuizItem {
	mk := func(id, slug, objective, family, q, correct string, opts [4]string, refs ...string) types.LearningQuizItem {
		options := types.QuizOptions{"A": opts[0], "B": opts[1], "C": opts[2], "D": opts[3]}
		var correctKey string
		for k, v := range options {
			if v == correct {
				correctKey = k
			}
		}
		return types.LearningQuizItem{
			ID: id, TenantID: tenant, KnowledgeBaseID: kb, Slug: slug,
			Question: q, Options: options, CorrectKey: correctKey,
			Explanation: "参考材料：" + slug + "。此题为修订草稿；内容负责人需逐项核实来源是否支持题干、答案与解析后再发布。", ChunkRefs: types.RefList(refs),
			ObjectiveID: objective, FamilyID: family,
			Status: types.LearningQuizStatusDraft,
		}
	}
	llmChunks := []string{"c88c60e6-0617-429c-8715-1fb7b7e959c9", "63865092-b9c3-4238-98a8-7d9e55c6576f"}
	return []types.LearningQuizItem{
		// obj-llm-init：两族
		mk("demo-item-llm-1", "concept/da-yu-yan-mo-xing", "obj-llm-init", "fam-llm-model",
			"ChatTongyi 的 model 参数（如 qwen-plus / qwen-turbo）决定什么？",
			"使用哪个模型", [4]string{"使用哪个模型", "采样温度", "API 密钥", "最大回复长度"}, llmChunks...),
		mk("demo-item-llm-2", "concept/da-yu-yan-mo-xing", "obj-llm-init", "fam-llm-parameter-boundary",
			"保持 model 不变，只降低 temperature，以下哪种解释正确？",
			"仍使用同一模型，改变采样随机性", [4]string{"自动切换到更大的模型", "仍使用同一模型，改变采样随机性", "重新训练模型权重", "更换 API 身份"}, llmChunks...),
		// obj-temp-direction：两族
		mk("demo-item-temp-1", "concept/wen-du-can-shu", "obj-temp-direction", "fam-temp-high",
			"temperature 取值越高，模型输出越趋于哪种状态？",
			"越发散", [4]string{"越稳定", "越发散", "越长", "越快"}, "c88c60e6-0617-429c-8715-1fb7b7e959c9", "5fb96730-9c50-4e13-b871-5bc4cb57b251"),
		mk("demo-item-temp-2", "concept/wen-du-can-shu", "obj-temp-direction", "fam-temp-high",
			"希望降低输出随机性时，temperature 通常应如何调整（不保证完全复现）？",
			"把值调低", [4]string{"把值调低", "把值调高", "设为负数", "保持 0.7 不变"}, "c88c60e6-0617-429c-8715-1fb7b7e959c9", "5fb96730-9c50-4e13-b871-5bc4cb57b251"),
		// obj-lc-role：两族
		mk("demo-item-lc-1", "entity/langchain", "obj-lc-role", "fam-lc-scope",
			"以下哪一项不属于 LangChain 在示例项目中的职责？",
			"训练大模型", [4]string{"提示词模板", "工具定义", "Agent 创建与调用", "训练大模型"}, "bf931ecd-c3c3-43d9-822a-ae8ec2751d91", "c88c60e6-0617-429c-8715-1fb7b7e959c9"),
		mk("demo-item-lc-2", "entity/langchain", "obj-lc-role", "fam-lc-orchestration",
			"需要组合提示词模板、模型接口和外部工具时，LangChain 负责哪一部分？",
			"编排这些组件之间的调用流程", [4]string{"编排这些组件之间的调用流程", "替代所有外部工具的业务逻辑", "直接拥有并修改模型权重", "保证模型每次回答都正确"}, "c88c60e6-0617-429c-8715-1fb7b7e959c9", "63865092-b9c3-4238-98a8-7d9e55c6576f"),
		// obj-tool-flow：两族
		mk("demo-item-tool-1", "concept/gong-ju-diao-yong", "obj-tool-flow", "fam-tool-diff",
			"与普通模型调用相比，工具调用的关键差异是什么？",
			"模型提出工具调用请求，由应用执行工具并回传结果", [4]string{"模型提出工具调用请求，由应用执行工具并回传结果", "模型生成函数名就等于外部操作已执行", "工具结果无需返回便会自动进入模型上下文", "模型权重会随每次工具调用重新训练"}, "bf931ecd-c3c3-43d9-822a-ae8ec2751d91", "afa72848-9ad4-4afb-b0be-eef9889a3c98"),
		mk("demo-item-tool-2", "concept/gong-ju-diao-yong", "obj-tool-flow", "fam-tool-return-flow",
			"应用已经执行完检索工具，但模型回答中没有检索结果。应优先检查哪一段流程？",
			"是否把工具结果作为后续输入交回模型", [4]string{"是否把工具结果作为后续输入交回模型", "是否把工具名改得更短", "是否把 temperature 调高", "是否去掉工具参数约束"}, "bf931ecd-c3c3-43d9-822a-ae8ec2751d91", "afa72848-9ad4-4afb-b0be-eef9889a3c98"),
		// obj-tools-three：两族
		mk("demo-item-tools-1", "entity/langchain-tools-py", "obj-tools-three", "fam-tools-func",
			"用 @tool 装饰的 calculator 函数属于哪一类工具？",
			"函数工具", [4]string{"函数工具", "工具绑定", "自定义工具类", "内置工具"}, "75b32328-953a-40a4-b1e4-d81a3223523c", "7063bc36-63b9-46d2-8ecb-8f052fa6af85"),
		mk("demo-item-tools-2", "entity/langchain-tools-py", "obj-tools-three", "fam-tools-convert",
			"LangChain 把函数工具转换为模型可识别形式时读取什么？",
			"函数名、参数类型和注释", [4]string{"函数名、参数类型和注释", "函数返回值", "调用栈", "全局配置文件"}, "75b32328-953a-40a4-b1e4-d81a3223523c", "84dcc765-f50c-4a51-85dc-28de1cae5903"),
		// obj-sql-position：两族
		mk("demo-item-sql-1", "entity/sqlite", "obj-sql-position", "fam-sql-kind",
			"SQLite 是哪一类数据库？",
			"轻量级本地数据库", [4]string{"轻量级本地数据库", "分布式搜索引擎", "向量数据库", "内存时序数据库"}, "910e75ad-f4de-4dce-8420-6baa7028dea3"),
		mk("demo-item-sql-2", "entity/sqlite", "obj-sql-position", "fam-sql-upgrade",
			"在学习文档的“可继续扩展方向”里，SQLite 的定位是什么？",
			"替代内存字典的知识库存储升级选项", [4]string{"替代内存字典的知识库存储升级选项", "模型训练数据集", "前端 UI 组件", "日志清理工具"}, "910e75ad-f4de-4dce-8420-6baa7028dea3"),
	}
}

func demoTasks(tenant uint64, kb string) []types.LearningTask {
	return []types.LearningTask{
		{
			ID: "demo-task-rag-1", TenantID: tenant, KnowledgeBaseID: kb,
			Slug: "concept/jian-suo-zeng-qiang-sheng-cheng", ObjectiveID: "obj-rag-upgrade",
			FamilyID: "fam-task-rag-upgrade",
			Title:    "知识库升级方案配置",
			Scenario: "小型单机客服知识库需要重启后保留资料，每次问题先查找相关片段，回答中保留可检查的依据。假设所选模型接口支持传入检索片段，请选择满足这三项约束的方案。该任务只检查方案选择，不检查程序是否实际运行。",
			Fields: types.JSONColumn(`[
                {"id":"storage","label":"单机持久化存储","type":"select","options":["进程内临时字典","SQLite 持久化数据库","仅保留模型聊天上下文"]},
                {"id":"flow","label":"查询与生成流程","type":"select","options":["先检索相关片段，再将片段随问题交给模型","直接回答，之后再搜索文档","把全部历史聊天当作唯一知识源"]},
                {"id":"trace","label":"回答依据","type":"select","options":["保留检索片段与源文档关联","只保存模型回答","只保存 temperature 参数"]}
            ]`),
			AnswerKey:      map[string]string{"storage": "SQLite 持久化数据库", "flow": "先检索相关片段，再将片段随问题交给模型", "trace": "保留检索片段与源文档关联"},
			CriticalChecks: types.JSONColumn(`[{"id":"storage","description":"满足重启后资料保留的约束"},{"id":"flow","description":"检索证据参与生成"},{"id":"trace","description":"保留可核验来源"}]`),
			SourceRefs:     types.JSONColumn(`["910e75ad-f4de-4dce-8420-6baa7028dea3"]`),
			ContentVersion: "v1", AssistanceMode: types.AssistanceOpenBook,
			Status: types.LearningQuizStatusDraft,
		},
	}
}
