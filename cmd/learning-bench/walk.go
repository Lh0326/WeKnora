package main

// walk mode: a simulated learner that walks the WHOLE topic-4 chain against
// a live WeKnora instance — recommendation → page read → timeline → mastery
// lighting → quiz (deterministic grading) → graph overlay → (optional) a
// real RAG question → export → delete + opt-out — asserting every link the
// design doc §4.2 demo chain names. This is the production-readiness
// patrol: every ✗ is a broken link, and the exit code is non-zero on any.
//
// The walker authenticates with an API key, so it is its OWN learning
// subject: the final delete wipes only the simulated learner's data and
// never touches a real user's profile.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	walkServer  = flag.String("server", "http://127.0.0.1:18081", "walk: base URL of the live instance")
	walkAPIKey  = flag.String("api-key", "", "walk: tenant API key (the simulated learner's identity)")
	walkKB      = flag.String("kb", "", "walk: knowledge base id (default: first KB of the walker)")
	walkSeed    = flag.Int64("seed", 42, "walk: RNG seed for the random study choices")
	walkAsk     = flag.String("ask", "", "walk: optional real RAG question to exercise the QA→citation chain")
	walkTimeout = flag.Int("walk-timeout", 180, "walk: per-step HTTP timeout in seconds")
)

type walkCheck struct {
	name string
	ok   bool
	note string
}

type walker struct {
	base   string
	client *http.Client
	rng    *rand.Rand
	checks []walkCheck
}

func (w *walker) record(name string, ok bool, format string, args ...interface{}) {
	w.checks = append(w.checks, walkCheck{name: name, ok: ok, note: fmt.Sprintf(format, args...)})
	mark := "✓"
	if !ok {
		mark = "✗"
	}
	fmt.Printf("  %s %-22s %s\n", mark, name, fmt.Sprintf(format, args...))
}

// do runs one JSON request and decodes the envelope's data field into out.
func (w *walker) do(method, path string, body interface{}, out interface{}) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, w.base+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-API-Key", *walkAPIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if out != nil {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if jerr := json.Unmarshal(raw, &env); jerr == nil && len(env.Data) > 0 {
			_ = json.Unmarshal(env.Data, out)
		}
	}
	return resp.StatusCode, raw, nil
}

func mapGet(m map[string]interface{}, key string) interface{} { return m[key] }

func runWalk() error {
	if *walkAPIKey == "" {
		return fmt.Errorf("walk: -api-key is required (mint one via the tenant API-keys endpoint)")
	}
	w := &walker{
		base:   strings.TrimRight(*walkServer, "/"),
		client: &http.Client{Timeout: time.Duration(*walkTimeout) * time.Second},
		rng:    rand.New(rand.NewSource(*walkSeed)),
	}
	fmt.Printf("模拟学习者巡检 · 目标 %s · seed=%d\n\n", w.base, *walkSeed)

	// ---- L1 health (the health endpoint lives at the root, not /api/v1) ----
	code, _, err := w.do("GET", "/health", nil, nil)
	w.record("L1 服务健康", err == nil && code == 200, "GET /health → %d", code)

	// ---- resolve KB ----
	if *walkKB == "" {
		var kbs []map[string]interface{}
		code, _, err = w.do("GET", "/api/v1/knowledgebase?page=1&page_size=10", nil, &kbs)
		if err != nil || code != 200 || len(kbs) == 0 {
			return fmt.Errorf("walk: cannot list knowledge bases (code=%d err=%v) — pass -kb explicitly", code, err)
		}
		*walkKB, _ = mapGet(kbs[0], "id").(string)
	}
	kb := *walkKB
	fmt.Printf("  · 知识库: %s\n\n", kb)

	// ---- L2 recommendations ----
	var recs []map[string]interface{}
	code, _, err = w.do("GET", "/api/v1/learning/kb/"+kb+"/recommend?limit=8", nil, &recs)
	w.record("L2 推荐列表", err == nil && code == 200 && len(recs) > 0, "GET recommend → %d, %d 张卡片", code, len(recs))
	if len(recs) == 0 {
		return walkFailed(w)
	}

	// ---- L3 pick a card (prefer one with quiz material) ----
	var pick map[string]interface{}
	var withQuiz []map[string]interface{}
	for _, r := range recs {
		if b, _ := mapGet(r, "has_quiz").(bool); b {
			withQuiz = append(withQuiz, r)
		}
	}
	pool := recs
	if len(withQuiz) > 0 {
		pool = withQuiz
	}
	pick = pool[w.rng.Intn(len(pool))]
	slug, _ := mapGet(pick, "slug").(string)
	title, _ := mapGet(pick, "title").(string)
	reason, _ := mapGet(pick, "reason").(string)
	w.record("L3 选卡(随机)", slug != "", "选中「%s」(%s, 理由=%s)", title, slug, reason)

	// ---- L4 the "查看" read ----
	code, _, err = w.do("POST", "/api/v1/learning/kb/"+kb+"/read", map[string]string{"slug": slug}, nil)
	w.record("L4 页面阅读(查看)", err == nil && code == 200, "POST read → %d", code)

	// ---- L5+L6 dedup + timeline visibility ----
	countReads := func() (int, int) {
		var tl struct {
			Data []map[string]interface{} `json:"-"`
		}
		var env struct {
			Data []map[string]interface{}
			Total int
		}
		_, rraw, _ := w.do("GET", "/api/v1/learning/kb/"+kb+"/timeline?page=1&page_size=100", nil, nil)
		_ = json.Unmarshal(rraw, &env)
		tl.Data = env.Data
		n, total := 0, env.Total
		for _, it := range tl.Data {
			if t, _ := mapGet(it, "event_type").(string); t == "wiki_tool_read" {
				if s, _ := mapGet(it, "slug").(string); s == slug {
					n++
				}
			}
		}
		return n, total
	}
	reads1, total1 := countReads()
	code, _, _ = w.do("POST", "/api/v1/learning/kb/"+kb+"/read", map[string]string{"slug": slug}, nil)
	reads2, _ := countReads()
	w.record("L5 重复阅读去重", reads2 == reads1 && reads1 >= 1, "48h 窗口内重复打开: %d → %d 条记录(不变=防刷分)", reads1, reads2)
	w.record("L6 时间线可见", reads1 >= 1, "时间线含「页面阅读」事件 ×%d (共 %d 条)", reads1, total1)

	// ---- L7 mastery lit by the read ----
	var views []map[string]interface{}
	code, _, err = w.do("GET", "/api/v1/learning/kb/"+kb+"/map", nil, &views)
	lit := false
	var evBefore float64
	var lvlBefore string
	for _, v := range views {
		if s, _ := mapGet(v, "slug").(string); s == slug {
			lvlBefore, _ = mapGet(v, "level").(string)
			evBefore, _ = mapGet(v, "evidence_count").(float64)
			if lvlBefore != "unseen" {
				lit = true
			}
		}
	}
	w.record("L7 阅读点亮节点", err == nil && code == 200 && lit, "「%s」档位=%s 证据=%d (≠未接触)", title, lvlBefore, int(evBefore))

	// ---- L8 quiz take (answer material must not leak) ----
	var questions []map[string]interface{}
	code, qraw, err := w.do("GET", "/api/v1/learning/kb/"+kb+"/quiz?slug="+slugPathEscape(slug), nil, &questions)
	leak := strings.Contains(string(qraw), "correct_key") || strings.Contains(string(qraw), "explanation")
	w.record("L8 取题不泄答案", err == nil && code == 200 && !leak && len(questions) > 0, "GET quiz → %d 题, 答案材料%s泄漏", len(questions), map[bool]string{true: "", false: "未"}[leak])

	// ---- L9+L10 answer: wrong first, then the correct key from the verdict ----
	quizDone := false
	if len(questions) > 0 {
		itemID, _ := mapGet(questions[0], "id").(string)
		var res1 map[string]interface{}
		code, _, err = w.do("POST", "/api/v1/learning/kb/"+kb+"/quiz/"+itemID+"/answer", map[string]string{"chosen_key": "A"}, &res1)
		correct1, _ := mapGet(res1, "correct").(bool)
		correctKey, _ := mapGet(res1, "correct_key").(string)
		w.record("L9 确定性判卷", err == nil && code == 200 && correctKey != "", "作答A → 判定=%v 正确键=%s", correct1, correctKey)

		var res2 map[string]interface{}
		w.do("POST", "/api/v1/learning/kb/"+kb+"/quiz/"+itemID+"/answer", map[string]string{"chosen_key": correctKey}, &res2)
		correct2, _ := mapGet(res2, "correct").(bool)
		// mastery must have moved and a review schedule must come back
		var views2 []map[string]interface{}
		w.do("GET", "/api/v1/learning/kb/"+kb+"/map", nil, &views2)
		moved := false
		for _, v := range views2 {
			if s, _ := mapGet(v, "slug").(string); s == slug {
				ev2, _ := mapGet(v, "evidence_count").(float64)
				moved = ev2 > evBefore
			}
		}
		_, tlraw, _ := w.do("GET", "/api/v1/learning/kb/"+kb+"/timeline?page=1&page_size=100", nil, nil)
		hasQuizEvent := strings.Contains(string(tlraw), "quiz_correct") || strings.Contains(string(tlraw), "quiz_wrong")
		sched, _ := mapGet(res2, "next_review_days").(float64)
		_, schedPresent := res2["next_review_days"]
		quizDone = correct2 && moved && hasQuizEvent
		w.record("L10 测验入流+折叠", quizDone, "答对=%v 证据 %d→增加=%v 时间线含测验事件=%v", correct2, int(evBefore), moved, hasQuizEvent)
		w.record("L10b 复习计划返回", schedPresent && (!correct2 || sched > 0), "next_review_days=%v", sched)
	} else {
		w.record("L9 确定性判卷", true, "（该节点暂无题，跳过——题库由 KB 开关生成的写路径维护）")
		w.record("L10 测验入流+折叠", true, "（无题可答，跳过）")
		w.record("L10b 复习计划返回", true, "（跳过）")
	}

	// ---- L11 graph overlay paints the node (bare {nodes} shape, no envelope) ----
	code, grow, err := w.do("GET", "/api/v1/knowledgebase/"+kb+"/wiki/graph?mode=overview&limit=500&with_mastery=true", nil, nil)
	var graph struct {
		Nodes []map[string]interface{} `json:"nodes"`
	}
	_ = json.Unmarshal(grow, &graph)
	painted := false
	for _, n := range graph.Nodes {
		if s, _ := mapGet(n, "slug").(string); s == slug {
			if ml, _ := mapGet(n, "mastery_level").(string); ml != "" {
				painted = true
			}
		}
	}
	w.record("L11 图谱掌握度叠加", err == nil && code == 200 && painted, "graph?with_mastery 节点带 mastery_level=%v", painted)

	// ---- L12 optional real RAG question → answer_cite chain ----
	if *walkAsk != "" {
		var sess map[string]interface{}
		code, _, err = w.do("POST", "/api/v1/sessions", map[string]interface{}{"title": "learning-walk 模拟学习者"}, &sess)
		sid, _ := mapGet(sess, "id").(string)
		if err != nil || code != 201 && code != 200 || sid == "" {
			w.record("L12 真实问答→引用点亮", false, "创建会话失败 code=%d err=%v", code, err)
		} else {
			qaErr := w.askQuestion(sid, kb)
			// poll the timeline for an answer_cite event
			cited := false
			var citeType string
			for i := 0; i < 30 && !cited; i++ {
				time.Sleep(3 * time.Second)
				var env struct {
					Data []map[string]interface{}
				}
				_, tlraw, _ := w.do("GET", "/api/v1/learning/kb/"+kb+"/timeline?page=1&page_size=100", nil, nil)
				if json.Unmarshal(tlraw, &env) == nil {
					for _, it := range env.Data {
						if t, _ := mapGet(it, "event_type").(string); t == "answer_cite" || t == "cross_ref" {
							cited = true
							citeType = t
						}
					}
				}
			}
			w.record("L12 真实问答→引用点亮", qaErr == nil && cited, "问答完成(%v) 引用事件=%s", qaErr, citeType)
		}
	}

	// ---- L13 export ----
	var export map[string]interface{}
	code, _, err = w.do("GET", "/api/v1/learning/export", nil, &export)
	events, _ := mapGet(export, "events").([]interface{})
	attempts, _ := mapGet(export, "quiz_attempts").([]interface{})
	exportOK := err == nil && code == 200 && len(events) >= 1
	if quizDone {
		exportOK = exportOK && len(attempts) >= 1
	}
	w.record("L13 导出画像", exportOK, "events=%d attempts=%d", len(events), len(attempts))

	// ---- L14 delete + opt-out ----
	code, _, err = w.do("DELETE", "/api/v1/learning/profile?opt_out=true", nil, nil)
	var views3 []map[string]interface{}
	w.do("GET", "/api/v1/learning/kb/"+kb+"/map", nil, &views3)
	w.do("POST", "/api/v1/learning/kb/"+kb+"/read", map[string]string{"slug": slug}, nil)
	var env4 struct {
		Data []map[string]interface{}
		Total int
	}
	_, tlraw4, _ := w.do("GET", "/api/v1/learning/kb/"+kb+"/timeline?page=1&page_size=100", nil, nil)
	json.Unmarshal(tlraw4, &env4)
	_, _ = views3, err
	w.record("L14 删除+停采防复活", code == 200 && env4.Total == 0, "删除后时间线=%d 条; 停采后补读不再落库", env4.Total)

	// ---- L15 re-enable collection (leave the key usable) ----
	code, _, err = w.do("PUT", "/api/v1/learning/settings", map[string]bool{"collect_disabled": false}, nil)
	w.record("L15 恢复采集开关", err == nil && code == 200, "PUT settings → %d", code)

	return walkFailed(w)
}

// askQuestion streams one RAG turn to completion (SSE consumed to EOF).
func (w *walker) askQuestion(sessionID, kb string) error {
	body := map[string]interface{}{
		"query":              *walkAsk,
		"knowledge_base_ids": []string{kb},
		"disable_title":      true,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", w.base+"/api/v1/knowledge-chat/"+sessionID, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", *walkAPIKey)
	req.Header.Set("Content-Type", "application/json")
	// stream timeout is longer than the per-step default
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("QA stream code=%d body=%.200s", resp.StatusCode, raw)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		// consume the stream; the learning hook fires server-side on completion
	}
	return sc.Err()
}

func walkFailed(w *walker) error {
	fmt.Println("\n  ---- 巡检结果 ----")
	failed := 0
	for _, c := range w.checks {
		mark := "✓"
		if !c.ok {
			mark = "✗"
			failed++
		}
		fmt.Printf("  %s %s\n", mark, c.name)
	}
	fmt.Printf("  合计: %d/%d 通过\n", len(w.checks)-failed, len(w.checks))
	if failed > 0 {
		return fmt.Errorf("walk: %d link(s) broken", failed)
	}
	return nil
}

func slugPathEscape(slug string) string {
	return strings.ReplaceAll(slug, "/", "%2F")
}

var _ = os.Stdout
