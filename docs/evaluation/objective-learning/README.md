# 课题四：独立评估的实际执行

本目录包含可重跑的工程夹具、结果和独立任务模板。`engineering-fixture*.json` 均为人工构造的工程数据，不是专家评分、真实用户或学习效果证据。示例结果故意包含错误：已验证失败率为 1/4，引导组与基线组后测差为 −25 分；不能将这些数值用作项目效果结论。

## 当前学习单元：按阶段做个人客观试用

脚本同时支持旧页面目标（`target_type=objective`，缺省值）和当前知识组件（`target_type=component`）。两者保留自己的状态，不把组件的 `familiar`（检查支持）改名为旧目标的 `verified`（已验证）。下面的个人流程只要求一位真实参与者，不需要把同一人的多个任务当成多人。

组件保留题库需在顶层声明 `target_type: "component"`；每题使用 `component_id`、`component_version`，其余 `family_id`、`phase`、`scenario`、`fields`、`answer_key`、来源、评分规则与审核声明沿用本目录模板。组件 ID 和版本必须来自实际导入的组件。题目先对照来源核对；仅有作者审核时如实声明，不能改填独立专家。可用 `material_origin` 单独说明合成素材，参与者的 `data_origin=real_user` 只说明实际有人参与，不会让合成素材变成真实业务案例。

学习前通过已登录产品的画像管理下载“评估快照”，无需向执行者提供登录令牌。下载文件保存原样，脚本不补造服务器字段：

```powershell
python scripts/learning_assessment.py capture --freeze-file <下载的学习前快照.json> --kb <知识库ID> --participant P001 --group guided --origin real_user --target-type component --phase pretest --block first-trial --bank <来源已核对的保留题库.json> --out <P001.pre.capture.json>
```

`--freeze-file` 与 `--base` 二选一；直接访问接口的旧方式继续可用。下载文件仍校验知识库、目标版本、来源可用性和已知题族暴露，但本地文件没有密码学签名，来源是执行者声明，哈希只帮助发现后续变化。快照保留原始组件状态、所有已提交检查涉及的题族（含旧版本和练习）及来源有效标记。已展示但未提交、线下见过的等价题仍未知，`component_exposure_complete=false`；执行者必须核实，不能把没有提交记录当作“从未见过”。

在前测、学习和后测完成之前，冻结题库、来源页面和组件定义，不编辑、不重新解析、不更新组件，也不把保留题导入日常检查库。若来源或版本变化，应停止本轮并保留原因，重新建立协议和试次，不能让新旧内容混用。后测必须等实际学习结束，再下载该时点的**新快照**、调用 `capture --phase posttest`、最后 `present`。不能提前呈现全部后测，或事后伪造学习结束时的状态。

按当前阶段逐题执行 `present`，独立展示公共题目；该命令不输出答案键。可以用仅含公共题目的外部答题页收集首答，不必增加产品内的实验平台：

```powershell
python scripts/learning_assessment.py present --capture <P001.pre.capture.json> --bank <保留题库.json> --item <前测题ID> --out <P001.pre.item.presented.json>
python scripts/learning_assessment.py grade --presentation <P001.pre.item.presented.json> --bank <保留题库.json> --answers <首答.json> --out <P001.pre.item.graded.json>
```

答案既可沿用 `{ "decision": "选项字符串" }`，也可使用本地答题页回执：

```json
{"answers":{"decision":"选项字符串"},"submitted_at":"2026-09-12T09:00:00Z","assisted":false}
```

每个必需字段只能提交展示过的选项。回执中的帮助标记与 `--assisted` 取“或”，不能用命令行覆盖成无帮助。脚本在快照旁创建 `.trials` 首次呈现/首答账本；换输出文件名或再次呈现不会重置时间、替换首答，同一答案重试保留原结果。保留整个账本，勿复制或编辑文件绕过它：这是可审计的本地操作约束，不是防篡改考试系统。

`response_wall_seconds` 是首次呈现至本地提交回执的间隔；没有回执时间时，以评分时刻作为上界。它会包含操作或中断，不是纯答题思考时间，更不是学习时长。学习阶段单独计时并记录中断；本项目的自动阅读阈值不能代替学习计时。缺失与额外帮助保留，不补成答错、不从报告中删除。

学习后完成新快照与不同任务结构的后测，再以同一个匿名 ID 生成描述性结果：

```powershell
python scripts/learning_assessment.py describe --captures <P001.pre.capture.json> <P001.post.capture.json> --grades <前测评分文件.json> <后测评分文件.json> --out <P001.description.json>
```

`describe` 支持同一个人的多个阶段或区块，按阶段列已分配、提交、合格、通过和缺失数量，保留原始状态与检查支持状态的外部后测分子/分母。只有该阶段全部已分配任务都完成且合格时才给完整总分；其余显示 `null`。任务需全部关键字段正确才算通过，字段级结果保存在原评分文件中。不会将题目当成独立用户，不输出群体效果、显著性或因果提升。仅改变数字或措辞不证明题族独立；即使前后测任务结构不同，也不自动具有相同难度或等价量表，所以默认只并列分数，不计算学习增益。长期保持需要另行延迟测。

`assemble` 可归档单次组件捕获的原始输入；同一人多次捕获请用 `describe`。旧 `go run ./cmd/learning-bench -mode assess` 仍只处理旧页面目标的多人评估，组件输入会明确拒绝，避免生成不适用的“已验证”指标。下面原有多人流程及工程夹具保持兼容。

## 两种证据回答两个问题

1. 工程验证：相同输入是否稳定、草稿和重复反馈是否不会验证、删除后是否不会补写。这些由代码测试回答。
2. 有效性验证：标为已验证的目标能否通过独立挑战、引导是否提高后测表现。这些必须使用独立题族、真实参与者或明确标为专家情境的数据回答。满意度只作补充，不能代替客观结果。

两者分开汇报。没有真人数据时，可以交付正确的工程结果与可执行评估协议，但不能写“已证明学习效率提升”。

## 先冻结协议与内容

在招募和查看正式后测之前，存一份带版本的协议：目标范围、内容版本、目标行为、纳入条件、样本量上限、基线、学习时间、允许的帮助、各阶段题族、主指标、缺失处理、统计方法、停止条件。保留原文件哈希。

建议先做 4–6 人可用性试用，定位任务理解与界面问题；修订后另取参与者做约 12–20 人探索性对照。这是资源可承受的原型方案，不是统计功效保证。小样本结果允许区间很宽或没有差异，应按事实报告。

正式对照以人为分组单位，使用固定种子随机分配；可按前测水平分层。在招募后、展示学习材料前保存分组表。引导组使用本项目路径，基线组使用同一批材料的目录导航；固定时间预算、同一目标范围、同等题目数量与作答条件。不要把用户的十个目标当成十个独立用户。

`holdout-bank.template.json` 默认是草稿，执行器会拒绝。内容负责人应填写真实目标 ID、当前内容版本、独立题族、来源、情境、字段、关键答案、评分规则和审核身份。它是研究者保管的题库，包含答案，不能交给参与者或导入日常练习库。模型可以帮助起草，不能代填人工签字。

训练、前测、后测、延迟测应使用不同题族。为了证明两个题族独立，审核者要检查是否只是同一题目的换词、正反问或选项重排；不同字符串或不同答案不是充分证据。确认普通题库中没有保留题的等价变体。

## 执行链路

先更新后端到包含 PostgreSQL 000101 / SQLite 000027 的代码和迁移版本。以下命令使用项目根目录，Python 只依赖标准库。每位参与者使用自己的登录令牌；令牌仅通过 `WEKNORA_EVAL_TOKEN` 环境变量传入，不写入文件。使用项目已有的登录流程获得令牌，不共享账号。真实用户试验先取得知情同意，并使用匿名参与者编号。

### 1. 冻结本人的状态

在学习阶段结束、任何保留挑战出现之前执行：

```powershell
python scripts/learning_assessment.py capture --base http://localhost:18081 --kb <知识库ID> --participant P001 --group guided --origin real_user --bank <已审核保留题库.json> --out <P001.freeze.json>
```

调用 `GET /api/v1/learning/kb/:kb_id/assessment-freeze`。身份与租户由后端权限链确定，客户端不能指定他人的 subject。后端使用与答题、删除相同的用户事务栅栏，冻结完整已审核且仍有效的目标集合、目标状态、内容版本、现有作答涉及的全部题族和服务端时间。包括错题、练习题族；不只返回通过题族。

快照不包含后测结果。输出文件独占创建，不允许覆盖。`data_origin` 是明确声明，不是系统自动认证“这个人是真人”。本地时间和哈希也不能证明未观察到的真实操作，应同时保留招募/同意记录、执行日志和原始文件。已展示但未作答、线下学习和其他系统的题族暴露仍需执行者核实。

### 2. 呈现独立挑战

```powershell
python scripts/learning_assessment.py present --capture <P001.freeze.json> --bank <已审核保留题库.json> --item <保留题ID> --out <P001.item1.presented.json>
```

先保存展示记录和时间，再在终端输出参与者需要看到的情境、选项与允许条件，不输出答案键。研究者保留题库。先检查冻结后的题库哈希、目标版本及已知题族暴露；不能冻结以后改题。

前测应单独执行前测阶段并保存；后测快照不能反推前测状态。若在同一评估输入记录多个阶段，评估器只把合格后测计入主状态指标，其他阶段保留作泄漏检查。延迟测应另冻结该时点状态并独立报告，不能混入当前主后测率。

### 3. 外部确定性评分

参与者的答案文件只包含字段 ID 到选项字符串的映射：

```json
{"decision":"选项一"}
```

```powershell
python scripts/learning_assessment.py grade --presentation <P001.item1.presented.json> --bank <已审核保留题库.json> --answers <P001.item1.answers.json> --out <P001.item1.graded.json>
```

若使用额外助手，添加 `--assisted`。关键检查逐项精确匹配，全部通过才算该任务通过。评分不调用 LLM，也不写回日常学习画像，因此不会先更新状态、再用更新后的状态评价自己。

不答、退出、缺失不能补成错误答案或从研究中删除；保留其冻结记录。重试不能替换第一次成绩。执行器禁止相同参与者同一挑战重复拼入数据。

### 4. 汇总为评估输入

```powershell
python scripts/learning_assessment.py assemble --captures <P001.freeze.json> <P002.freeze.json> --grades <P001.item1.graded.json> <P002.item1.graded.json> --out <assessment-input.json> --seed 20260910

go run ./cmd/learning-bench -mode assess -assess-input <assessment-input.json> -assess-out <assessment-report.json>
```

缺失后测者仍在入组人数及缺失统计中；只有全部已分配后测都完成且合格时，脚本才输出该参与者的后测总分。对退出者在正式研究输入保留 `withdrawn=true`、`missing_reason`，原始文件保留。调参数据使用 `--tuning` 并与正式评估分开；看过正式结果后修改规则，应另立版本、另收数据。

脚本当前一次 capture 对应一个评估时点。若要比较前测、后测和延迟测，不应把同一人的多次 capture 冒充多个参与者；分别生成时点报告，再以匿名 ID 配对分析。前后测增益不能用不等量表原始分直接相减。

## 指标与解释

| 指标 | 分子 / 分母 | 注意 |
|---|---|---|
| 已验证失败率 | 已验证状态的合格独立后测失败 / 对应后测总数 | 核对“验证”是否过度乐观；不等于未来答对概率 |
| 未验证通过率 | 未验证状态的合格独立后测通过 / 对应后测总数 | 找出可能过度保守的目标 |
| 验证覆盖 | 全部冻结目标中的已验证数 / 全部冻结目标数 | 缺完整目标集合则 N/A，不能用被抽测目标充当全库分母 |
| 后测差 | 引导组人均标准化分 − 基线组人均标准化分 | 只有实施了合理分配与控制，才讨论引导效果；单纯差值不是因果证明 |
| 缺失 / 退出 | 按组列入组、完成、缺失、退出人数及原因 | 完成者分析可能有偏，必须并列呈现 |
| 主观体验 | 路径清晰度、解释是否符合预期、自由探索体验 | 补充解释客观结果，不替代后测 |

时间顺序必须满足 `state_as_of <= presented_at < graded_at`。重复参与者、身份不一致、无效阶段、错误分数范围、混合数据来源被拒绝；题族泄漏、不合格作答、练习和缺失从对应率中排除，并留下 `exclusions`。每位用户每个目标选择最早合格后测，状态和成绩来自同一条记录。

原始率按用户–目标计数，同时以用户为簇做 bootstrap 区间，保留同一人的目标相关性。后测差按用户 bootstrap；某组不足两人、或某状态只有一名用户时不输出貌似精确的区间。极小样本、相同数值造成窄区间或零宽区间都不是普遍可靠性的证明。

## 工程演示重跑

```powershell
go run ./cmd/learning-bench -mode assess -assess-input docs/evaluation/objective-learning/engineering-fixture.json -assess-out <新的结果文件.json>
python -m unittest discover -s scripts -p test_learning_assessment.py -v
```

提交时附上规则版本、原始匿名数据、所有排除原因、脚本、结果和限制。若尚无真人数据，明确填“真实用户有效性：未执行”，保留本目录的工程结果即可，不要改标签充当真人。
