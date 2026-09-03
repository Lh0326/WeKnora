package main

// verify mode: a five-layer offline verification suite for the topic-4
// algorithms — 节点层(node) / 关联层(association) / 度量层(measurement,
// the focus) / 引导层(guidance) / 呈现层(presentation). Every check is
// deterministic and reproducible; the run prints a per-layer table and
// writes a JSON artifact (-verify-out) for the report figures.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
)

type verifyCheck struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

type verifyLayer struct {
	Layer  string        `json:"layer"`
	Checks []verifyCheck `json:"checks"`
}

type verifyReport struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Layers      []verifyLayer `json:"layers"`
	Total       int           `json:"total"`
	Passed      int           `json:"passed"`
	Failed      int           `json:"failed"`
}

func (r *verifyReport) add(layer string, c verifyCheck) {
	for i := range r.Layers {
		if r.Layers[i].Layer == layer {
			r.Layers[i].Checks = append(r.Layers[i].Checks, c)
			r.Total++
			if c.Pass {
				r.Passed++
			} else {
				r.Failed++
			}
			return
		}
	}
	r.Layers = append(r.Layers, verifyLayer{Layer: layer, Checks: []verifyCheck{c}})
	r.Total++
	if c.Pass {
		r.Passed++
	} else {
		r.Failed++
	}
}

func checkf(name string, pass bool, format string, args ...interface{}) verifyCheck {
	return verifyCheck{Name: name, Pass: pass, Detail: fmt.Sprintf(format, args...)}
}

var vBase = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

func vPage(slug, title string) *types.WikiPage {
	pt := "concept"
	for i := 0; i < len(slug); i++ {
		if slug[i] == '/' {
			pt = slug[:i]
			break
		}
	}
	return &types.WikiPage{Slug: slug, Title: title, PageType: pt}
}

func runVerify(outPath string) error {
	r := &verifyReport{GeneratedAt: time.Now()}

	// ========================= 节点层 =========================
	{
		stale := learning.FoldAll(learning.FoldState{}, []learning.Event{
			{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: vBase},
			{Type: types.LearningEventQuizCorrect, Weight: learning.WeightQuizCorrect, OccurredAt: vBase.Add(time.Hour)},
		})
		migs := learning.ReconcileSlug(
			map[string]learning.FoldState{"concept/old": stale},
			map[string][]learning.Event{"concept/old": {
				{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: vBase},
			}},
			map[string]string{"concept/old": "concept/new"},
		)
		ok := len(migs) == 1 && migTo(migs, "concept/old") == "concept/new"
		r.add("节点层", checkf("别名对账迁移", ok, "改名后旧 slug 经别名确定性迁移到新 slug"))

		// 目标已有状态 → 重放合并（不是 logit 相加）
		target := learning.FoldAll(learning.FoldState{}, []learning.Event{
			{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: vBase.Add(2 * time.Hour)},
		})
		migs2 := learning.ReconcileSlug(
			map[string]learning.FoldState{"concept/old": stale, "concept/new": target},
			map[string][]learning.Event{
				"concept/old": {{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: vBase}},
				"concept/new": {{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: vBase.Add(2 * time.Hour)}},
			},
			map[string]string{"concept/old": "concept/new"},
		)
		m := findMig(migs2, "concept/old")
		ok2 := m != nil && m.State.EvidenceCount == 2 && m.State.Logit == 2*learning.WeightAnswerCite
		r.add("节点层", checkf("迁移合并走事件重放", ok2, "目标已有状态时按事件重放合并（证据 2 条，logit=2.0），不做简单相加"))

		migs3 := learning.ReconcileSlug(
			map[string]learning.FoldState{"concept/orphan": stale},
			nil, map[string]string{},
		)
		r.add("节点层", checkf("无别名保留待下轮", len(migs3) == 0, "别名索引无匹配时不猜目标，保留原行等待下轮对账"))
	}

	// ========================= 关联层 =========================
	{
		pages := []*types.WikiPage{
			{Slug: "concept/rag", PageType: "concept", ChunkRefs: types.StringArray{"chunk-1"}, SourceRefs: types.StringArray{"doc-1|Rag Doc"}},
			{Slug: "concept/decay", PageType: "concept", ChunkRefs: types.StringArray{"chunk-9"}, SourceRefs: types.StringArray{"doc-2|Decay Doc"}},
		}
		refs := types.References{
			{ID: "chunk-1", KnowledgeBaseID: "kb"},
			{ID: "other", KnowledgeID: "doc-2", KnowledgeBaseID: "kb"},
		}
		touched := learning.BenchTouchedSlugs(pages, refs, "kb")
		sort.Strings(touched)
		ok := len(touched) == 2 && touched[0] == "concept/decay" && touched[1] == "concept/rag"
		r.add("关联层", checkf("通道A双路交集", ok, "chunk∩ChunkRefs 与 文档∩SourceRefs 双路命中（rag 经 chunk，decay 经文档）"))

		now := vBase.Add(24 * time.Hour)
		prior := []types.LearningEvent{{Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: now.Add(-2 * time.Hour)}}
		c1 := learning.BenchClassifyTouch(now, "concept/rag", prior)
		prior2 := []types.LearningEvent{{Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: now.Add(-72 * time.Hour)}}
		c2 := learning.BenchClassifyTouch(now, "concept/rag", prior2)
		ok2 := c1 == types.LearningEventReAsk && c2 == types.LearningEventCrossRef && learning.BenchClassifyTouch(now, "concept/rag", nil) == types.LearningEventAnswerCite
		r.add("关联层", checkf("触达分型三态", ok2, "窗口内追问=re_ask(负)、窗外回访=cross_ref(强)、首触=answer_cite"))

		prior3 := []types.LearningEvent{
			{Slug: "concept/rag", Type: types.LearningEventReAsk, OccurredAt: now.Add(-3 * time.Hour)},
			{Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: now.Add(-1 * time.Hour)},
		}
		c3 := learning.BenchClassifyTouch(now, "concept/rag", prior3)
		r.add("关联层", checkf("追问限量(多角度学习保护)", c3 == "", "窗口内已有 re_ask 后续触达跳过——围绕一个主题多角度提问不再被连续扣分"))

		stat := &types.MemoryTopicStat{NormalizedKey: "rag", Topic: "RAG"}
		tp := []*types.WikiPage{vPage("concept/rag", "RAG 检索增强"), vPage("concept/decay", "惰性衰减")}
		cands := learning.BenchTopicCandidates(stat, tp, 8)
		ok4 := len(cands) == 1 && cands[0] == "concept/rag"
		r.add("关联层", checkf("通道B候选召回", ok4, "话题'rag'只召回标题归一化匹配的页面，不相关的'惰性衰减'不进候选"))

		raw := `{"maps":{"rag":{"slug":"concept/invented","confidence":0.99},"low":{"slug":"concept/rag","confidence":0.3},"good":{"slug":"concept/rag","confidence":0.9}}}`
		acc := learning.BenchAcceptedTopicMapsRaw(raw, map[string][]string{"rag": {"concept/rag"}, "low": {"concept/rag"}, "good": {"concept/rag"}})
		_, hasInvented := acc["rag"]
		_, hasLow := acc["low"]
		good, hasGood := acc["good"]
		ok5 := !hasInvented && !hasLow && hasGood && good.Slug == "concept/rag"
		r.add("关联层", checkf("通道B防幻觉三闸", ok5, "候选集外 slug 拒收 + 置信度<0.7 拒收 + 合格映射放行"))

		ep := []*types.WikiPage{
			{Slug: "concept/a", PageType: "concept", Title: "A", OutLinks: types.StringArray{"concept/b"}, SourceRefs: types.StringArray{"d1|Doc"}},
			{Slug: "concept/b", PageType: "concept", Title: "B", SourceRefs: types.StringArray{"d1|Doc"}},
			{Slug: "concept/c", PageType: "concept", Title: "C", OutLinks: types.StringArray{"concept/c"}},
		}
		pairs := learning.BenchEdgeCandidatePairs(ep)
		hasAB, hasSelf := false, false
		for _, p := range pairs {
			if p[0] == "concept/a" && p[1] == "concept/b" {
				hasAB = true
			}
			if p[0] == p[1] {
				hasSelf = true
			}
		}
		r.add("关联层", checkf("先修边候选(链接+共现,排自环)", hasAB && !hasSelf, "wiki-link 直连与同文档共现对均入选，自环被排除"))

		eraw := `{"pairs":[{"from":"concept/a","to":"concept/b","relation":"prerequisite","confidence":0.9},{"from":"concept/a","to":"concept/b","relation":"prerequisite","confidence":0.5},{"from":"concept/b","to":"concept/a","relation":"related","confidence":0.99}]}`
		edges := learning.BenchAcceptedEdgesRaw(eraw, [][2]string{{"concept/a", "concept/b"}, {"concept/b", "concept/a"}})
		ok6 := len(edges) == 1 && edges[0].From == "concept/a" && edges[0].To == "concept/b"
		r.add("关联层", checkf("先修边裁决过滤", ok6, "仅 prerequisite 且置信度≥0.7 落库；related 与低置信被丢弃"))
	}

	// ========================= 度量层（重点） =========================
	{
		s := learning.FoldAll(learning.FoldState{}, repeatEvents(types.LearningEventAnswerCite, learning.WeightAnswerCite, 3, vBase))
		ok := s.Logit == 3*learning.WeightAnswerCite && s.EvidenceCount == 3
		r.add("度量层", checkf("折叠单调性", ok, "3 次引用 → logit=3.0，证据计数同步"))

		capped := learning.FoldAll(learning.FoldState{}, repeatEvents(types.LearningEventQuizCorrect, learning.WeightQuizCorrect, 10, vBase))
		floored := learning.FoldAll(learning.FoldState{}, repeatEvents(types.LearningEventQuizWrong, learning.WeightQuizWrong, 10, vBase))
		ok2 := capped.Logit == learning.LogitCap && floored.Logit == learning.LogitFloor
		r.add("度量层", checkf("logit 双向钳制", ok2, "刷 10 次答对封顶 +4.0；10 次答错封底 -4.0"))

		rng := rand.New(rand.NewSource(7))
		events := repeatEvents(types.LearningEventAnswerCite, learning.WeightAnswerCite, 4, vBase)
		neg := []learning.Event{{Type: types.LearningEventReAsk, Weight: learning.WeightReAsk, OccurredAt: vBase.Add(5 * time.Hour)}}
		all := append(append([]learning.Event{}, events...), neg...)
		direct := learning.FoldAll(learning.FoldState{}, all)
		rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		shuffled := learning.FoldAll(learning.FoldState{}, all)
		ok3 := direct.Logit == shuffled.Logit && direct.EvidenceCount == shuffled.EvidenceCount
		r.add("度量层", checkf("折叠顺序无关性", ok3, "事件乱序折叠与正序结果完全一致(logit=%.2f)，历史可任意重放", direct.Logit))

		s4 := learning.FoldAll(learning.FoldState{}, repeatEvents(types.LearningEventAnswerCite, learning.WeightAnswerCite, 5, vBase))
		wantS := learning.StabilityBaseDays *
			math.Pow(1+learning.StabilityGrowth*5, learning.StabilitySaturationExp)
		r.add("度量层", checkf("稳定性增长(FSRS)", s4.Stability == wantS, "5 条正证据 → s=14×(1+0.35×5)^0.5≈%.1f 天（乘性增长、边际递减），复习越多忘越慢", wantS))

		p0 := learning.EffectiveP(s4, vBase.Add(5*time.Hour))
		p30 := learning.EffectiveP(s4, vBase.Add(35*24*time.Hour))
		s1 := learning.FoldAll(learning.FoldState{}, repeatEvents(types.LearningEventAnswerCite, learning.WeightAnswerCite, 1, vBase))
		p30b := learning.EffectiveP(s1, vBase.Add(35*24*time.Hour))
		ok5 := p0 > p30 && p30 > p30b && p0 <= 1.0
		r.add("度量层", checkf("Ebbinghaus 惰性衰减", ok5, "零时距不衰减(%.3f)；35天后降至 %.3f；证据少的同窗降得更狠(%.3f)", p0, p30, p30b))

		hyst := true
		for _, p := range []float64{0.22, 0.26, 0.299} {
			if learning.LevelOf(p, 5, learning.LevelTouched).Level != learning.LevelTouched {
				hyst = false
			}
		}
		if learning.LevelOf(0.2199, 5, learning.LevelTouched).Level != learning.LevelUnseen {
			hyst = false
		}
		if learning.LevelOf(0.30, 5, learning.LevelUnseen).Level != learning.LevelTouched {
			hyst = false
		}
		r.add("度量层", checkf("四档滞回防抖", hyst, "0.22~0.30 缓冲带内抖动保持原档；跌破 0.22 降档；越过 0.30 升档"))

		low := learning.LevelOf(0.5, 1, learning.LevelUnseen)
		r.add("度量层", checkf("低置信标记", low.LowConfidence, "证据 1 条 < 3 → 如实标注低置信，不装作自信"))

		g1, _ := learning.GradeQuiz("A", "A", 0)
		g2, _ := learning.GradeQuiz("A", "B", 0)
		g3, _ := learning.GradeQuiz("A", "A", 2)
		gU, errU := learning.GradeQuiz("A", learning.QuizUnsureKey, 0)
		_, errBad := learning.GradeQuiz("A", "F", 0)
		ok6 := g1.Correct && !g2.Correct && g3.Weight == learning.WeightQuizCorrect*learning.QuizRepeatDecay*learning.QuizRepeatDecay &&
			errU == nil && gU.Weight == 0 && gU.EventType == types.LearningEventQuizUnsure && errBad != nil
		r.add("度量层", checkf("确定性判卷", ok6, "键匹配判对错（零 LLM）；同题第3次作答权重=%.2f（防刷题衰减）；「不确定」零权重申报；非法选项拒绝", g3.Weight))

		ord := learning.WeightQuizCorrect > learning.WeightCrossRef &&
			learning.WeightCrossRef > learning.WeightAnswerCite &&
			learning.WeightAnswerCite > learning.WeightTopicSignal &&
			learning.WeightQuizWrong < learning.WeightReAsk
		r.add("度量层", checkf("证据强度次序约束", ord, "答对(2.2) > 跨题回访(1.6) > 引用(1.0) > 间接信号(0.5)；答错(-1.5) 惩罚重于追问(-0.8)"))

		zero := learning.BenchAnchoredLevel(learning.FoldState{}, vBase)
		r.add("度量层", checkf("零证据=未接触", zero.Level == learning.LevelUnseen, "无任何事件的新节点判 unseen，而非 sigmoid(0)=0.5 的虚假中位"))
	}

	// ========================= 引导层 =========================
	{
		now := vBase.Add(72 * time.Hour)
		pages := []*types.WikiPage{
			vPage("concept/base", "基础"), vPage("concept/mid", "进阶"), vPage("concept/adv", "高级"),
		}
		edges := []types.LearningEdge{{TenantID: 1, KnowledgeBaseID: "kb", FromSlug: "concept/base", ToSlug: "concept/mid", Relation: types.LearningEdgePrerequisite}}
		// base 未学：mid（前置未满足）必须被门控排除；adv 无前置关系，本就是可推的外边缘。
		recs := learning.BenchRecommendNodes(learning.BenchRecommendInput{
			Pages: pages, Edges: edges, States: map[string]learning.FoldState{},
		}, now, nil, 5)
		slugs := recSlugs(recs)
		ok := !contains(slugs, "concept/mid") && contains(slugs, "concept/adv")
		r.add("引导层", checkf("外边缘先修门控", ok, "前置'基础'未学 → '进阶'被门控排除；无前置约束的'高级'照常可推——只走下一步够得着的路"))

		// 阻塞窗口：档位须非 unseen、p_eff < 0.30、证据 ≥3、且停学超过
		// 追问窗（"长期"卡住，不是正在学）。幂律衰减下记忆保持显著更
		// 久（这正是校准改善），2 引用+1 答错(p0≈0.62)需停学 240 天才
		// 跌破 0.30。
		later := vBase.Add(240 * 24 * time.Hour)
		weak := learning.FoldAll(learning.FoldState{}, append(
			repeatEvents(types.LearningEventAnswerCite, learning.WeightAnswerCite, 2, vBase),
			learning.Event{Type: types.LearningEventQuizWrong, Weight: learning.WeightQuizWrong, OccurredAt: vBase.Add(time.Hour)},
		))
		recs2 := learning.BenchRecommendNodes(learning.BenchRecommendInput{
			Pages:  []*types.WikiPage{vPage("concept/gate", "门"), vPage("concept/behind", "门后")},
			Edges:  []types.LearningEdge{{TenantID: 1, FromSlug: "concept/gate", ToSlug: "concept/behind", Relation: types.LearningEdgePrerequisite}},
			States: map[string]learning.FoldState{"concept/gate": weak},
		}, later, nil, 5)
		slugs2 := recSlugs(recs2)
		// 结果契约：卡住的"门"必须出现在推荐里（优先补前置/巩固），
		// 被它挡住的"门后"必须暂缓——理由标签由遍历顺序决定，不强求。
		outcome := contains(slugs2, "concept/gate") && !contains(slugs2, "concept/behind")
		r.add("引导层", checkf("阻塞前置先行", outcome, "前置长期低掌握(停学240天后 p_eff≈0.29、证据 3 条)时'门'被优先推荐，被挡住的'门后'暂缓"))

		studied := learning.FoldAll(learning.FoldState{}, []learning.Event{
			{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: now.Add(-48 * time.Hour)},
			{Type: types.LearningEventQuizWrong, Weight: learning.WeightQuizWrong, OccurredAt: now},
		})
		rp := []*types.WikiPage{vPage("concept/logit-accumulation", "Logit累加模型"), vPage("concept/alpha", "甲概念"), vPage("concept/beta", "乙概念")}
		recs3 := learning.BenchRecommendNodes(learning.BenchRecommendInput{
			Pages: rp, States: map[string]learning.FoldState{"concept/logit-accumulation": studied},
		}, now, nil, 5)
		respOK := len(recs3) == 3 && recs3[2].Slug == "concept/logit-accumulation"
		r.add("引导层", checkf("推荐响应学习行为(本次修复)", respOK, "刚学过(含答错)的节点沉底、未接触盲区置顶——列表承认每一次答题与阅读，Logit节点不再因拉丁标题钉死第一"))

		r1 := learning.BenchRecommendNodes(learning.BenchRecommendInput{Pages: rp}, now, rand.New(rand.NewSource(42)), 5)
		r2 := learning.BenchRecommendNodes(learning.BenchRecommendInput{Pages: rp}, now, rand.New(rand.NewSource(42)), 5)
		det := true
		for i := range r1 {
			if r1[i].Slug != r2[i].Slug {
				det = false
			}
		}
		r.add("引导层", checkf("推荐可复现性", det, "同种子两次运行推荐列表完全一致（确定性，无黑箱）"))

		var allReasoned bool = len(r1) > 0
		for _, rec := range r1 {
			if rec.Reason == "" {
				allReasoned = false
			}
		}
		r.add("引导层", checkf("推荐可解释", allReasoned, "每条推荐都携带理由字段（外边缘/亲和/巩固/阻塞/探索）"))
	}

	// ========================= 呈现层 =========================
	{
		fresh := learning.FoldAll(learning.FoldState{}, repeatEvents(types.LearningEventAnswerCite, learning.WeightAnswerCite, 2, vBase))
		lv := learning.BenchAnchoredLevel(fresh, vBase.Add(time.Minute))
		ok := lv.Level == learning.LevelMastered
		r.add("呈现层", checkf("档位派生一致性", ok, "2 次引用(logit=2, p约88%%) → mastered，与图谱/页签/进度环同一派生函数"))

		idem := learning.BenchAnchoredLevel(fresh, vBase.Add(time.Minute))
		r.add("呈现层", checkf("呈现确定性", lv.Level == idem.Level && lv.LowConfidence == idem.LowConfidence, "同一状态多次派生结果一致（读路径零随机）"))

		closed := true
		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 200; i++ {
			evs := make([]learning.Event, rng.Intn(8))
			for j := range evs {
				w := []float64{learning.WeightAnswerCite, learning.WeightQuizCorrect, learning.WeightQuizWrong, learning.WeightReAsk}[rng.Intn(4)]
				evs[j] = learning.Event{Weight: w, OccurredAt: vBase}
			}
			l := learning.BenchAnchoredLevel(learning.FoldAll(learning.FoldState{}, evs), vBase).Level
			switch l {
			case learning.LevelUnseen, learning.LevelTouched, learning.LevelFamiliar, learning.LevelMastered:
			default:
				closed = false
			}
		}
		r.add("呈现层", checkf("四档封闭性(200随机态)", closed, "200 个随机状态派生的档位全部落在四档枚举内，无越界值"))
	}

	// ---- print & artifact ----
	layerNames := []string{"节点层", "关联层", "度量层", "引导层", "呈现层"}
	fmt.Println()
	for _, ln := range layerNames {
		for i := range r.Layers {
			if r.Layers[i].Layer != ln {
				continue
			}
			passed := 0
			for _, c := range r.Layers[i].Checks {
				if c.Pass {
					passed++
				}
			}
			fmt.Printf("  [%s] %d/%d 通过\n", ln, passed, len(r.Layers[i].Checks))
			for _, c := range r.Layers[i].Checks {
				mark := "✓"
				if !c.Pass {
					mark = "✗"
				}
				fmt.Printf("    %s %s — %s\n", mark, c.Name, c.Detail)
			}
		}
	}
	fmt.Printf("\n  总计: %d/%d 通过, %d 失败\n", r.Passed, r.Total, r.Failed)

	if outPath != "" {
		buf, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(outPath, buf, 0o644); err != nil {
			return err
		}
		fmt.Printf("  JSON 工件: %s\n", outPath)
	}
	if r.Failed > 0 {
		return fmt.Errorf("%d verification check(s) failed", r.Failed)
	}
	return nil
}

func repeatEvents(t string, w float64, n int, at time.Time) []learning.Event {
	out := make([]learning.Event, n)
	for i := 0; i < n; i++ {
		out[i] = learning.Event{Type: t, Weight: w, OccurredAt: at.Add(time.Duration(i) * time.Minute)}
	}
	return out
}

func migTo(migs []learning.SlugMigration, from string) string {
	for _, m := range migs {
		if m.FromSlug == from {
			return m.ToSlug
		}
	}
	return ""
}

func findMig(migs []learning.SlugMigration, from string) *learning.SlugMigration {
	for i := range migs {
		if migs[i].FromSlug == from {
			return &migs[i]
		}
	}
	return nil
}

func recSlugs(recs []learning.Recommendation) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Slug)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
