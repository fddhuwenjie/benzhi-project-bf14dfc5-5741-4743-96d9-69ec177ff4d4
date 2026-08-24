# 馆藏文物保存环境异常处置台

面向中小型博物馆保管团队的单流程工作台，覆盖环境异常登记、风险研判、负责人分派、措施执行、效果复核和关闭/退回，并保留事件时间线。服务由 Go 原生 HTTP 提供浏览器工作台和 JSON API，数据保存在本地 JSON 快照。

登记时可以提交同一指标的多条 `measured_at` 读数，并用 `phase=baseline|abnormal` 区分基线与异常。服务统一换算温度、湿度、光照和污染物单位，严格校验基线早于同指标异常、证据时间有效且单位可比较；缺少基线时返回 `missing_baseline_metrics`，不会补造数值。`POST /api/incidents` 设置 `preflight=true` 可执行不落库的登记预检，一次返回字段错误、规范化预览、基线配对、连续区间、风险依据、规则命中和响应时限。

登记会冻结 `rule_set_version`、阈值快照和逐指标 `rule_hits`，包括实际值、阈值边界、持续时长及敏感级别加分，后续读取和复核不会用新规则覆盖历史研判。待分派事件可通过 `POST /api/incidents/{id}/readings-correction` 提交 `reading_id`、`replacement_reading`、`reason`、`revision` 和 `request_id`；旧读数及证据保留替换链，风险与期限按冻结规则重新计算。

正式登记会检索同区域、同指标和相近观测时间的活动事件。完全重复时返回 `exact_duplicate`，可能关联时返回 `related_confirmation_required`；后者可补充 `independent_reason` 后确认登记，候选编号和理由会保存在登记时间线中。已关闭候选只作为历史参考。

分派、措施完成、提交复核和复核决定都要求稳定的 `request_id`。相同操作和内容的网络重试会返回第一次成功结果且不增加修订号；跨事件、跨操作或内容变化会返回 `idempotency_conflict` 及当前状态和修订号。分派会检查执行人的活动任务和期限冲突，高风险冲突需通过 `continue_reason` 说明继续分派，并将负荷快照冻结到方案中。

处置中事件可继续使用 `assignment` 端点提交 `transfer_assignee`、`transfer_reason` 和新 `due_at` 完成交接。交接保留前后执行人、期限及已完成措施，重新检查工作量冲突，并以修订号和请求标识保证幂等。

措施项可通过 `prerequisite_ids` 引用同一方案的前置项，循环、自引用和不存在的引用会在分派前被拒绝。`items` 端点既支持单项，也支持包含多个 `{item_id,note,effect_readings}` 的 `items` 数组；整批先校验唯一性、依赖、读数和证据，再只增加一次修订号。每个措施项的同指标复测会生成按时间排序的趋势、相邻变化、恢复比例和稳定性；多次复测重新越界或间隔不足两小时时，`retest_metrics` 会阻止提交复核。

复核必须提交与服务端当前比较集一致的 `confirmed_reading_ids`，且复核人不得是当前轮次执行人或措施记录人。退回复核会冻结当前处置轮次并生成下一轮整改项；合格关闭会原子生成版本化归档摘要，包含全部轮次、最终有效读数、证据、责任人、关键时间和逾期结论，并返回稳定内容校验值及校验状态。

`GET /api/incidents` 支持 `status`、`area_id`、`risk_level`、`deadline_bucket`、`observed_from` 和 `observed_to` 交集过滤，时间使用 RFC3339。响应同时返回固定排序的状态、风险和区域维度统计，包含数量、逾期数、平均响应秒数与关闭率。`GET /api/incidents/{id}` 返回期限、责任链、处置轮次、读数对比和稳定性；关闭事件使用 `view=archive` 查询只读归档报告，查询会复算校验和，内容不一致返回 `data_integrity_error`，非关闭事件返回 `invalid_state`。时间线继续支持筛选和游标分页，读取会同时校验 JSON 快照与 JSONL 日志。

登记请求可以增加 `affected_items`，每项包含 `collection_id`、`material`、`quantity`、`sensitivity` 和 `impact_note`。服务按行校验编号唯一性、正数数量、材质与敏感级别，并生成 `affected_scope` 摘要；总体 `sensitivity` 必须等于清单最高级别。清单、最高级别触发藏品编号及归档内容在事件创建后冻结。收到 `related_confirmation_required` 后，可向 `POST /api/incidents/{id}/observations` 提交目标事件的 `expected_revision`、`readings`、`association_note`、`actor` 和 `request_id`。补充观测按原事件阈值快照复算，只允许风险维持或升级，并以一次条件提交保存读数和风险时间线。

待分派事件还可从同一 `observations` 入口提交一个或多个明确标记为 `phase=baseline` 的历史基线。请求必须使用 `expected_revision` 和稳定的 `request_id`；每条基线须能与现有有效异常指标配对，测量时间早于对应异常，且读数编号、证据引用和证据时间有效。成功后服务使用事件冻结的阈值快照重算基线配对、连续异常区间、风险依据和响应期限，并追加“基线补录”时间线事件；整次请求校验失败时不写入任何读数，相同请求重试不增加修订号。

`GET /api/incidents` 可继续叠加 `collection_id`、`material` 和藏品 `sensitivity` 条件，三者与状态、区域、风险等级及观测时间共同做交集筛选。`statistics.filters` 回显实际条件，并通过 `matching_incident_count`、`affected_item_rows`、`affected_total_quantity` 和 `by_material` 返回匹配事件数、藏品行数、总件数及材质数量；数量只计算符合藏品条件的当前有效清单行，无匹配时返回空事件数组和零值汇总。

待分派事件使用 `POST /api/incidents/{id}/assignment-preview` 提交二至三个 `candidates`，预览会固定排序返回指标覆盖、依赖、期限和执行人负荷问题及 `checksum`。确认仍提交到 `assignment`，携带相同候选内容、`selected_candidate_id`、`preview_checksum`、`expected_revision` 和 `request_id`。批量分派使用 `POST /api/incidents/batch-assignment`，请求包含 `entries`（事件编号和各自修订号）、统一执行人、`due_at` 或 `due_after_seconds`、措施模板 `items` 及批次请求标识；`preflight=true` 仅预检，正式提交全有或全无，成功事件共享 `batch_id`。

处置中的公开操作继续位于同一事件下：`plan-change` 接收 `plan_change.add|update|cancel`、原因和审批负责人；`process-records`（也可使用 `/items/{item_id}/records`）追加开始、检查点、问题或问题解决记录；带 `process_record_sequences` 的 `items` 完成请求将最终效果读数与过程记录关联。`deadline-change-request` 由当前执行人申请新期限，`deadline-change-decision` 由负责人批准或驳回；审批前 `due_at` 与逾期统计保持原值。

待复核事件的 `verification` 请求可提交 `metric_decisions`。每个异常指标都必须给出合格/不合格、当前有效比较读数、现场说明和对应有效证据；全部合格才能关闭，任一不合格只能退回，且下一轮整改项仅覆盖失败指标。每轮逐项结果保存在轮次、时间线与最终归档中。上述新增写操作均接受 `expected_revision`（兼容原 `revision`）并要求稳定的 `request_id`。

## 构建、运行与测试

```text
go test ./...
go vet ./...
go build ./cmd/server
go run ./cmd/server -addr=127.0.0.1:19081 -self-check
go run ./cmd/server -addr=127.0.0.1:19081
```

也可设置 `PORT`（仅端口号）让服务绑定 `127.0.0.1:<PORT>`。浏览器访问 `http://127.0.0.1:19081/`。

扩展接口：待分派事件可调用 `POST /api/incidents/{id}/assessment-preview` 与 `assessment-confirm` 预览并确认阈值规则重评估；`readings-invalidation` 撤回失效登记证据；`assignee-recommendations` 返回容量排序并支持携带推荐校验值确认分派。处置阶段可用 `review-preflight` 锁定复核比较集，过程异常通过 `escalation-confirm` 完成整改恢复。`GET /api/trends?recurrence_window_days=30` 返回按区域和指标的复发、响应、稳定及逾期统计。
