package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type API struct {
	Svc  *workflow.Service
	Repo domain.Repository
	Mux  *http.ServeMux
}

func New(s *workflow.Service, r domain.Repository) *API {
	a := &API{Svc: s, Repo: r, Mux: http.NewServeMux()}
	a.routes()
	return a
}

func (a *API) routes() {
	a.Mux.HandleFunc("/", a.page)
	a.Mux.HandleFunc("/static/app.js", a.js)
	a.Mux.HandleFunc("/api/incidents", a.incidents)
	a.Mux.HandleFunc("/api/incidents/batch-assignment", a.batchAssignment)
	a.Mux.HandleFunc("/api/incidents/assignment-batch", a.batchAssignment)
	a.Mux.HandleFunc("/api/archive", a.archive)
	a.Mux.HandleFunc("/api/trends", a.trends)
	a.Mux.HandleFunc("/api/handover/snapshots", a.handoverSnapshots)
	a.Mux.HandleFunc("/api/handover/sign", a.handoverSign)
	a.Mux.HandleFunc("/api/incidents/", a.incidentAction)
}

func (a *API) handoverSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Status    domain.Status    `json:"status"`
		AreaID    string           `json:"area_id"`
		RiskLevel domain.RiskLevel `json:"risk_level"`
		From      string           `json:"from"`
		To        string           `json:"to"`
		Shift     string           `json:"shift"`
		RequestID string           `json:"request_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		a.writeErr(w, "", err)
		return
	}
	snap, err := a.Svc.HandoverSnapshot(domain.IncidentFilter{Status: in.Status, AreaID: in.AreaID, RiskLevel: in.RiskLevel}, in.From, in.To, in.Shift, in.RequestID)
	if err != nil {
		a.writeErr(w, "", err)
		return
	}
	writeJSON(w, snap, http.StatusOK)
}
func (a *API) handoverSign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Snapshot            domain.HandoverSnapshot `json:"snapshot"`
		RequestID, From, To string
		Revisions           map[string]int `json:"revisions"`
	}
	if err := decodeJSON(r, &in); err != nil {
		a.writeErr(w, "", err)
		return
	}
	snap, err := a.Svc.SignHandover(in.Snapshot, in.RequestID, in.From, in.To, in.Revisions)
	if err != nil {
		a.writeErr(w, "", err)
		return
	}
	writeJSON(w, snap, http.StatusOK)
}

func (a *API) archive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	f := workflow.ArchiveFilter{Status: domain.Status(q.Get("status")), AreaID: strings.TrimSpace(q.Get("area_id")), RiskLevel: domain.RiskLevel(q.Get("risk_level")), Metric: strings.TrimSpace(q.Get("metric")), Evidence: strings.TrimSpace(q.Get("evidence"))}
	items, err := a.Svc.SearchArchive(f)
	if err != nil {
		a.writeErr(w, "", err)
		return
	}
	writeJSON(w, map[string]interface{}{"archives": items}, http.StatusOK)
}

func (a *API) trends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	gran := q.Get("granularity")
	if gran != "" && gran != "day" && gran != "week" {
		a.writeErr(w, "", &domain.ValidationError{Field: "granularity", Message: "统计粒度只能为 day 或 week"})
		return
	}
	filter := domain.IncidentFilter{Status: domain.Status(q.Get("status")), AreaID: q.Get("area_id"), RiskLevel: domain.RiskLevel(q.Get("risk_level"))}
	var from, to time.Time
	var err error
	if q.Get("from") != "" {
		from, err = time.Parse(time.RFC3339, q.Get("from"))
	}
	if err == nil && q.Get("to") != "" {
		to, err = time.Parse(time.RFC3339, q.Get("to"))
	}
	if err != nil {
		a.writeErr(w, "", &domain.ValidationError{Field: "time_range", Message: "时间范围必须为 RFC3339 格式"})
		return
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		a.writeErr(w, "", &domain.ValidationError{Field: "time_range", Message: "from 不得晚于 to"})
		return
	}
	if q.Get("recurrence_window") != "" || q.Get("recurrence_window_days") != "" {
		windowText := q.Get("recurrence_window_days")
		if windowText == "" {
			windowText = q.Get("recurrence_window")
		}
		window, parseErr := strconv.Atoi(windowText)
		if parseErr != nil {
			a.writeErr(w, "", &domain.ValidationError{Field: "recurrence_window", Message: "复发窗口必须为正整数"})
			return
		}
		if from.IsZero() {
			from = time.Now().UTC().Add(-30 * 24 * time.Hour)
		}
		if to.IsZero() {
			to = time.Now().UTC()
		}
		stats, statsErr := a.Svc.ClosureStats(from, to, strings.TrimSpace(q.Get("area_id")), strings.TrimSpace(q.Get("metric")), domain.RiskLevel(q.Get("risk_level")), window)
		if statsErr != nil {
			a.writeErr(w, "", statsErr)
			return
		}
		writeJSON(w, stats, http.StatusOK)
		return
	}
	writeJSON(w, a.Svc.Trends(gran, filter, from, to), http.StatusOK)
}

func (a *API) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = template.Must(template.New("page").Parse(indexHTML)).Execute(w, nil)
}

func (a *API) js(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(appJS + appPatchJS))
}

const appPatchJS = `
q('#correct').onsubmit=async e=>{e.preventDefault();const f=new FormData(e.target),response=await fetch('/api/incidents/'+encodeURIComponent(selected)),incident=await response.json(),original=(incident.readings||[]).find(x=>x.id===f.get('reading_id')&&!x.replaced_by_id);if(!original){q('#actionResult').className='error';q('#actionResult').textContent='未找到有效登记读数';return}const now=new Date().toISOString();request('/api/incidents/'+encodeURIComponent(selected)+'/readings-correction',{revision:Number(f.get('revision')),reading_id:f.get('reading_id'),replacement_reading:{metric:f.get('metric'),value:Number(f.get('value')),unit:f.get('unit'),measured_at:original.measured_at,source_note:'勘误复核',evidence_ref:f.get('evidence'),evidence_recorded_at:now},reason:f.get('reason'),actor:'保管员',request_id:crypto.randomUUID()})};
const extensionHTML='<details><summary>补充观测与基线</summary><form id="observation"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>阶段<select name="phase"><option value="abnormal">异常观测</option><option value="baseline">历史基线</option></select></label><label>关联说明<input name="note"></label><label>读数标识<input name="reading_id" required></label><label>指标<select name="metric"><option>温度</option><option>湿度</option><option>光照</option><option>污染物</option></select></label><label>读数值<input name="value" type="number" step="any" required></label><label>单位<input name="unit" required></label><label>测量时间<input name="measured_at" type="datetime-local" required></label><label>证据引用<input name="evidence" required></label><label>证据记录时间<input name="evidence_recorded_at" type="datetime-local" required></label><label>保管员<input name="actor" required></label></div><p><button>提交读数</button></p></form></details>'+ 
'<details><summary>措施方案比选</summary><form id="comparison"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>异常指标<input name="metrics" placeholder="温度,湿度" required></label><label>方案一执行人<input name="a1" required></label><label>方案一期限<input name="d1" type="datetime-local" required></label><label>方案一措施<input name="m1" required></label><label>方案一说明<input name="r1" required></label><label>方案二执行人<input name="a2" required></label><label>方案二期限<input name="d2" type="datetime-local" required></label><label>方案二措施<input name="m2" required></label><label>方案二说明<input name="r2" required></label><label>确认方案<select name="selected"><option value="">仅预览</option><option value="candidate-1">方案一</option><option value="candidate-2">方案二</option></select></label><label>负责人<input name="actor"></label></div><p><button>预览或确认</button></p></form></details>'+
'<details><summary>处置方案变更</summary><form id="planChange"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>操作<select name="mode"><option value="add">新增</option><option value="update">修改</option><option value="cancel">取消</option></select></label><label>措施项 ID<input name="item_id" required></label><label>措施说明<input name="description"></label><label>覆盖指标<input name="metrics"></label><label>前置项<input name="dependencies"></label><label>变更或取消原因<input name="reason" required></label><label>负责人<input name="approver" required></label></div><p><button>提交变更</button></p></form></details>'+
'<details><summary>措施过程台账</summary><form id="processRecord"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>措施项 ID<input name="item_id" required></label><label>类型<select name="type"><option>开始</option><option>检查点</option><option>问题</option><option>问题解决</option></select></label><label>说明<input name="note" required></label><label>证据引用<input name="evidence"></label><label>执行人<input name="actor" required></label></div><p><button>追加记录</button></p></form></details>'+
'<details><summary>处置期限变更</summary><form id="deadlineRequest"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>待审批期限<input name="due" type="datetime-local" required></label><label>延期原因<input name="reason" required></label><label>影响措施 ID<input name="items" required></label><label>申请人<input name="applicant" required></label></div><p><button>申请延期</button></p></form><form id="deadlineDecision"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>决定<select name="decision"><option>批准</option><option>驳回</option></select></label><label>决定说明<input name="note" required></label><label>负责人<input name="actor" required></label></div><p><button>提交决定</button></p></form></details>'+
'<details><summary>复核指标判定</summary><form id="metricVerification"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>指标<input name="metric" required></label><label>逐项判定<select name="metric_decision"><option>合格</option><option>不合格</option></select></label><label>确认读数 ID<input name="readings" required></label><label>现场说明<input name="note" required></label><label>证据引用<input name="evidence" required></label><label>总体结论<select name="decision"><option>合格</option><option>退回</option></select></label><label>复核人<input name="reviewer" required></label><label>总体说明<input name="reason"></label></div><p><button>提交复核</button></p></form></details>';
q('.actions').insertAdjacentHTML('beforeend',extensionHTML);
q('#create .grid').insertAdjacentHTML('beforebegin','<div id="affectedRows"><div class="grid affected-row"><label>藏品编号<input name="collection_id"></label><label>材质<select name="material"><option>纸质</option><option>陶器</option><option>纺织品</option><option>金属</option><option>木质</option><option>石质</option><option>玻璃</option><option>复合材质</option><option>其他</option></select></label><label>数量<input name="quantity" type="number" min="1" value="1"></label><label>敏感级别<select name="item_sensitivity"><option>高</option><option>中</option><option>低</option></select></label><label>影响说明<input name="impact_note"></label></div></div><p><button id="addAffected" type="button" class="secondary">添加藏品行</button></p>');
q('#addAffected').onclick=()=>{const row=q('.affected-row').cloneNode(true);row.querySelectorAll('input').forEach(input=>{if(input.name==='quantity')input.value='1';else input.value=''});q('#affectedRows').append(row)};
q('#list').insertAdjacentHTML('beforebegin','<details><summary>批量分派</summary><form id="batchAssignment"><div class="grid"><label>统一执行人<input name="assignee" required></label><label>期限<input name="due" type="datetime-local" required></label><label>措施说明<input name="description" required></label><label>覆盖指标<input name="metrics" required></label><label>负责人<input name="actor" required></label></div><p><button name="mode" value="preview">预检</button> <button name="mode" value="confirm">确认分派</button></p></form></details>');
q('#risk').closest('label').insertAdjacentHTML('afterend','<label>藏品编号<input id="collectionFilter"></label><label>材质<select id="materialFilter"><option value="">全部</option><option>纸质</option><option>陶器</option><option>陶瓷</option><option>纺织品</option><option>金属</option><option>木质</option><option>石质</option><option>玻璃</option><option>复合材质</option><option>其他</option></select></label><label>藏品敏感级别<select id="itemSensitivityFilter"><option value="">全部</option><option>高</option><option>中</option><option>低</option></select></label>');
q('#eventType').insertAdjacentHTML('beforeend','<option>基线补录</option>');
const csv=v=>v.split(',').map(x=>x.trim()).filter(Boolean),baseFetch=window.fetch.bind(window),baseLoad=load;let latestItemStatistics=null;
window.fetch=async(input,init)=>{let target=input;if(typeof input==='string'){const url=new URL(input,window.location.origin);if(url.pathname==='/api/incidents'&&(!init||!init.method||init.method==='GET')){const filters={collection_id:q('#collectionFilter').value.trim(),material:q('#materialFilter').value,sensitivity:q('#itemSensitivityFilter').value};Object.entries(filters).forEach(([key,value])=>{if(value)url.searchParams.set(key,value);else url.searchParams.delete(key)});target=url.pathname+'?'+url.searchParams.toString()}}const response=await baseFetch(target,init);if(typeof target==='string'&&target.startsWith('/api/incidents?')){const data=await response.clone().json();latestItemStatistics=data.statistics||null}return response};
load=async()=>{await baseLoad();if(latestItemStatistics){const materials=Object.entries(latestItemStatistics.by_material||{}).map(([key,value])=>key+' '+value+' 件').join(' · '),summary='藏品 '+latestItemStatistics.affected_item_rows+' 行 / '+latestItemStatistics.affected_total_quantity+' 件';q('#dimensions').textContent+=(q('#dimensions').textContent?' · ':'')+summary+(materials?' · '+materials:'')}};load();
q('#observation').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target),phase=f.get('phase');request('/api/incidents/'+encodeURIComponent(selected)+'/observations',{expected_revision:Number(f.get('revision')),association_note:f.get('note'),readings:[{id:f.get('reading_id'),phase,metric:f.get('metric'),value:Number(f.get('value')),unit:f.get('unit'),measured_at:iso(f.get('measured_at')),source_note:phase==='baseline'?'历史监测基线':'现场补充观测',evidence_ref:f.get('evidence'),evidence_recorded_at:iso(f.get('evidence_recorded_at'))}],actor:f.get('actor'),request_id:crypto.randomUUID()})};
let assignmentPreview=null;q('#comparison').onsubmit=async e=>{e.preventDefault();const f=new FormData(e.target),metrics=csv(f.get('metrics')),candidates=[1,2].map(n=>({id:'candidate-'+n,assignee:f.get('a'+n),due_at:iso(f.get('d'+n)),summary:f.get('m'+n),selection_reason:f.get('r'+n),items:[{id:'candidate-'+n+'-item',description:f.get('m'+n),covered_metrics:metrics}]})),selectedID=f.get('selected'),body={expected_revision:Number(f.get('revision')),candidates};if(selectedID){body.selected_candidate_id=selectedID;body.preview_checksum=assignmentPreview&&assignmentPreview.checksum;body.actor=f.get('actor');body.request_id=crypto.randomUUID()}const r=await fetch('/api/incidents/'+encodeURIComponent(selected)+'/assignment',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}),d=await r.json();if(r.ok&&!selectedID)assignmentPreview=d;q('#actionResult').className=r.ok?'':'error';q('#actionResult').textContent=JSON.stringify(d,null,2);if(r.ok&&selectedID){load();detail(selected)}};
q('#planChange').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target),mode=f.get('mode'),id=f.get('item_id'),change={add:[],update:[],cancel:[]};if(mode==='add')change.add=[{id,description:f.get('description'),covered_metrics:csv(f.get('metrics')),prerequisite_ids:csv(f.get('dependencies'))}];if(mode==='update')change.update=[{item_id:id,description:f.get('description'),covered_metrics:csv(f.get('metrics')),prerequisite_ids:csv(f.get('dependencies'))}];if(mode==='cancel')change.cancel=[{item_id:id,reason:f.get('reason')}];request('/api/incidents/'+encodeURIComponent(selected)+'/plan-change',{expected_revision:Number(f.get('revision')),plan_change:change,reason:f.get('reason'),approver:f.get('approver'),request_id:crypto.randomUUID()})};
q('#processRecord').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);request('/api/incidents/'+encodeURIComponent(selected)+'/items/'+encodeURIComponent(f.get('item_id'))+'/records',{expected_revision:Number(f.get('revision')),type:f.get('type'),occurred_at:nowISO(),note:f.get('note'),evidence_ref:f.get('evidence'),actor:f.get('actor'),request_id:crypto.randomUUID()})};
q('#deadlineRequest').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);request('/api/incidents/'+encodeURIComponent(selected)+'/deadline-change-request',{expected_revision:Number(f.get('revision')),requested_due_at:iso(f.get('due')),reason:f.get('reason'),affected_item_ids:csv(f.get('items')),applicant:f.get('applicant'),request_id:crypto.randomUUID()})};
q('#deadlineDecision').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);request('/api/incidents/'+encodeURIComponent(selected)+'/deadline-change-decision',{expected_revision:Number(f.get('revision')),decision:f.get('decision'),decision_note:f.get('note'),actor:f.get('actor'),request_id:crypto.randomUUID()})};
q('#metricVerification').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);request('/api/incidents/'+encodeURIComponent(selected)+'/verification',{expected_revision:Number(f.get('revision')),reviewer:f.get('reviewer'),decision:f.get('decision'),reason:f.get('reason'),metric_decisions:[{metric:f.get('metric'),decision:f.get('metric_decision'),confirmed_reading_ids:csv(f.get('readings')),note:f.get('note'),evidence_ref:f.get('evidence')}],request_id:crypto.randomUUID()})};
q('#batchAssignment').onsubmit=async e=>{e.preventDefault();const f=new FormData(e.target),entries=[...document.querySelectorAll('.batchPick:checked')].map(x=>({incident_id:x.value,expected_revision:Number(x.dataset.revision)})),body={entries,assignee:f.get('assignee'),due_at:iso(f.get('due')),summary:f.get('description'),items:[{id:'batch-template-item',description:f.get('description'),covered_metrics:csv(f.get('metrics'))}],actor:f.get('actor'),request_id:crypto.randomUUID(),preflight:e.submitter.value==='preview'},r=await fetch('/api/incidents/batch-assignment',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}),d=await r.json();q('#actionResult').className=r.ok?'':'error';q('#actionResult').textContent=JSON.stringify(d,null,2);if(r.ok&&!body.preflight)load()};
`

func writeJSON(w http.ResponseWriter, value interface{}, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *API) incidents(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("metric") != "" || r.URL.Query().Get("evidence") != "" {
			items, err := a.Svc.SearchArchive(workflow.ArchiveFilter{Status: domain.Status(r.URL.Query().Get("status")), AreaID: r.URL.Query().Get("area_id"), RiskLevel: domain.RiskLevel(r.URL.Query().Get("risk_level")), Metric: r.URL.Query().Get("metric"), Evidence: r.URL.Query().Get("evidence")})
			if err != nil {
				a.writeErr(w, "", err)
				return
			}
			writeJSON(w, map[string]interface{}{"archives": items}, http.StatusOK)
			return
		}
		filter, err := parseIncidentFilter(r)
		if err != nil {
			a.writeErr(w, "", err)
			return
		}
		writeJSON(w, a.Svc.List(filter), http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ID                       string                          `json:"id"`
		AreaID                   string                          `json:"area_id"`
		AffectedScope            string                          `json:"affected_scope"`
		Sensitivity              string                          `json:"sensitivity"`
		Actor                    string                          `json:"actor"`
		RequestID                string                          `json:"request_id"`
		ObservedAt               string                          `json:"observed_at"`
		Readings                 []domain.EnvironmentalReading   `json:"readings"`
		Preflight                bool                            `json:"preflight"`
		IndependentReason        string                          `json:"independent_reason"`
		ThresholdTemplateVersion string                          `json:"threshold_template_version"`
		AffectedItems            []domain.AffectedCollectionItem `json:"affected_items"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, map[string]interface{}{"code": "invalid_json", "error": "请求格式错误", "detail": err.Error()}, http.StatusBadRequest)
		return
	}
	observedAt, err := time.Parse(time.RFC3339, input.ObservedAt)
	if err != nil {
		if !input.Preflight {
			writeValidation(w, &domain.ValidationError{Field: "observed_at", Message: "观测时间必须为 RFC3339 格式"})
			return
		}
		observedAt = time.Time{}
	}
	command := workflow.CreateCommand{ID: input.ID, AreaID: input.AreaID, AffectedScope: input.AffectedScope, Sensitivity: input.Sensitivity, ThresholdTemplateVersion: input.ThresholdTemplateVersion, Actor: input.Actor, RequestID: input.RequestID, IndependentReason: input.IndependentReason, ObservedAt: observedAt, SubmittedAt: time.Now().UTC(), Readings: input.Readings, AffectedItems: input.AffectedItems}
	if input.Preflight {
		preview := a.Svc.Preflight(command)
		if err != nil {
			preview.Errors = append([]domain.FieldIssue{{Field: "observed_at", Message: "观测时间必须为 RFC3339 格式"}}, preview.Errors...)
			preview.Valid = false
		}
		writeJSON(w, preview, http.StatusOK)
		return
	}
	in, err := a.Svc.CreateContext(r.Context(), command)
	if err != nil {
		a.writeErr(w, input.ID, err)
		return
	}
	writeJSON(w, in, http.StatusCreated)
}

func (a *API) incidentAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && r.Method == http.MethodGet {
		if r.URL.Query().Get("view") == "archive" {
			archive, err := a.Svc.GetArchive(parts[2])
			if err != nil {
				a.writeErr(w, parts[2], err)
				return
			}
			writeJSON(w, archive, http.StatusOK)
			return
		}
		filter, filterErr := parseTimelineFilter(r)
		if filterErr != nil {
			a.writeErr(w, parts[2], filterErr)
			return
		}
		in, err := a.Svc.GetTimeline(parts[2], filter)
		if err != nil {
			a.writeErr(w, parts[2], err)
			return
		}
		writeJSON(w, in, http.StatusOK)
		return
	}
	pathItemID := ""
	if len(parts) == 6 && parts[3] == "items" && parts[5] == "records" {
		pathItemID = parts[4]
		parts = []string{parts[0], parts[1], parts[2], "process-records"}
	}
	if len(parts) != 4 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id, action := parts[2], parts[3]
	var input struct {
		Revision                 int                             `json:"revision"`
		ExpectedRevision         int                             `json:"expected_revision"`
		AreaID                   string                          `json:"area_id"`
		Assignee                 string                          `json:"assignee"`
		DueAt                    string                          `json:"due_at"`
		Summary                  string                          `json:"summary"`
		Items                    json.RawMessage                 `json:"items"`
		OverdueNote              string                          `json:"overdue_note"`
		Actor                    string                          `json:"actor"`
		RequestID                string                          `json:"request_id"`
		ItemID                   string                          `json:"item_id"`
		Note                     string                          `json:"note"`
		EffectReadings           []domain.EnvironmentalReading   `json:"effect_readings"`
		Reviewer                 string                          `json:"reviewer"`
		Decision                 string                          `json:"decision"`
		Reason                   string                          `json:"reason"`
		IndependentReason        string                          `json:"independent_reason"`
		ContinueReason           string                          `json:"continue_reason"`
		Correction               bool                            `json:"correction"`
		CorrectionReason         string                          `json:"correction_reason"`
		ConfirmedReadingIDs      []string                        `json:"confirmed_reading_ids"`
		TransferAssignee         string                          `json:"transfer_assignee"`
		TransferReason           string                          `json:"transfer_reason"`
		ReadingID                string                          `json:"reading_id"`
		ReplacementReading       domain.EnvironmentalReading     `json:"replacement_reading"`
		PauseReason              string                          `json:"pause_reason"`
		ExpectedResumeAt         string                          `json:"expected_resume_at"`
		StartedAt                string                          `json:"started_at"`
		ResumedAt                string                          `json:"resumed_at"`
		ReturnCategory           string                          `json:"return_category"`
		Approve                  bool                            `json:"approve"`
		Readings                 []domain.EnvironmentalReading   `json:"readings"`
		AssociationNote          string                          `json:"association_note"`
		Candidates               []domain.AssignmentCandidate    `json:"candidates"`
		SelectedCandidateID      string                          `json:"selected_candidate_id"`
		PreviewChecksum          string                          `json:"preview_checksum"`
		CandidateChecksum        string                          `json:"candidate_checksum"`
		PlanChange               domain.PlanChange               `json:"plan_change"`
		Approver                 string                          `json:"approver"`
		ProcessRecord            domain.ProcessRecord            `json:"process_record"`
		RecordType               string                          `json:"type"`
		OccurredAt               string                          `json:"occurred_at"`
		Reading                  *domain.EnvironmentalReading    `json:"reading"`
		EvidenceRef              string                          `json:"evidence_ref"`
		ProcessRecordSequences   []int                           `json:"process_record_sequences"`
		RequestedDueAt           string                          `json:"requested_due_at"`
		AffectedItemIDs          []string                        `json:"affected_item_ids"`
		Applicant                string                          `json:"applicant"`
		DecisionNote             string                          `json:"decision_note"`
		MetricDecisions          []domain.MetricVerification     `json:"metric_decisions"`
		AcceptanceStandards      []domain.AcceptanceStandard     `json:"acceptance_standards"`
		RetestCheckpoints        []domain.RetestCheckpoint       `json:"retest_checkpoints"`
		AffectedItems            []domain.AffectedCollectionItem `json:"affected_items"`
		EvidenceReadings         []domain.EnvironmentalReading   `json:"evidence_readings"`
		TemplateVersion          string                          `json:"template_version"`
		CandidateTemplateVersion string                          `json:"candidate_template_version"`
		Explanation              string                          `json:"explanation"`
		ReadingIDs               []string                        `json:"reading_ids"`
		InvalidReadingIDs        []string                        `json:"invalid_reading_ids"`
		ComparisonChecksum       string                          `json:"comparison_checksum"`
		CorrectionNote           string                          `json:"correction_note"`
		Assignees                []string                        `json:"assignees"`
		SelectedAssignee         string                          `json:"selected_assignee"`
		RecommendationChecksum   string                          `json:"recommendation_checksum"`
		Confirm                  bool                            `json:"confirm"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, map[string]interface{}{"code": "invalid_json", "error": "请求格式错误", "detail": err.Error()}, http.StatusBadRequest)
		return
	}
	hasExpectedRevision := input.ExpectedRevision > 0
	if input.Revision == 0 {
		input.Revision = input.ExpectedRevision
	}
	if input.TemplateVersion == "" {
		input.TemplateVersion = input.CandidateTemplateVersion
	}
	if len(input.ReadingIDs) == 0 {
		input.ReadingIDs = input.InvalidReadingIDs
	}
	if input.PreviewChecksum == "" {
		input.PreviewChecksum = input.CandidateChecksum
	}
	if pathItemID != "" {
		input.ItemID = pathItemID
	}
	var in *domain.PreservationIncident
	var err error
	switch action {
	case "assessment-preview":
		if input.Confirm || input.PreviewChecksum != "" {
			in, err = a.Svc.ConfirmReassessment(id, input.Revision, input.TemplateVersion, input.PreviewChecksum, input.Actor, input.RequestID, input.Explanation)
			break
		}
		preview, e := a.Svc.PreviewReassessment(id, input.Revision, input.TemplateVersion)
		if e == nil {
			writeJSON(w, preview, http.StatusOK)
			return
		}
		err = e
	case "assessment-confirm":
		in, err = a.Svc.ConfirmReassessment(id, input.Revision, input.TemplateVersion, input.PreviewChecksum, input.Actor, input.RequestID, input.Explanation)
	case "readings-invalidation", "readings-invalidate":
		in, err = a.Svc.InvalidateReadings(id, input.Revision, input.ReadingIDs, input.Reason, input.EvidenceRef, input.Actor, input.RequestID)
	case "assignee-recommendations":
		var due time.Time
		due, err = time.Parse(time.RFC3339, input.DueAt)
		if err == nil && input.SelectedAssignee == "" {
			var p workflow.RecommendationPreview
			p, err = a.Svc.RecommendAssignees(id, input.Revision, input.Assignees, due)
			if err == nil {
				writeJSON(w, p, http.StatusOK)
				return
			}
		}
		if err == nil && input.SelectedAssignee != "" {
			items := []domain.MitigationItem{}
			if len(input.Items) > 0 {
				if e := decodeRawItems(input.Items, &items); e != nil {
					err = e
				}
			}
			if err == nil {
				in, err = a.Svc.ConfirmAssigneeRecommendation(id, input.Revision, input.SelectedAssignee, due, input.RecommendationChecksum, input.Summary, items, input.Actor, input.RequestID, input.ContinueReason)
			}
		}
	case "review-preflight":
		var lock domain.ReviewLock
		lock, err = a.Svc.ReviewPreflight(id, input.Revision)
		if err == nil {
			writeJSON(w, lock, http.StatusOK)
			return
		}
	case "escalation-confirm", "process-escalation-confirm":
		in, err = a.Svc.ConfirmProcessEscalation(id, input.Revision, input.CorrectionNote, input.Actor, input.RequestID)
	case "observations", "additional-observations":
		if hasBaselineReading(input.Readings) && !hasExpectedRevision {
			err = &domain.ValidationError{Field: "expected_revision", Message: "基线补录必须提交 expected_revision"}
			break
		}
		in, err = a.Svc.AddObservationInArea(id, input.Revision, input.AreaID, input.Readings, input.AssociationNote, input.Actor, input.RequestID)
	case "affected-items", "affected-supplement":
		in, err = a.Svc.SupplementAffectedItems(id, input.Revision, input.AffectedItems, input.AssociationNote, input.EvidenceReadings, input.Actor, input.RequestID)
	case "retest-plan", "retest-checkpoints":
		in, err = a.Svc.SetRetestCheckpoints(id, input.Revision, input.RetestCheckpoints, input.Actor, input.RequestID)
	case "assignment-preview":
		var preview domain.AssignmentPreview
		preview, err = a.Svc.PreviewAssignment(id, input.Revision, input.Candidates)
		if err == nil {
			writeJSON(w, preview, http.StatusOK)
			return
		}
	case "assignment":
		if len(input.Candidates) > 0 && input.SelectedCandidateID == "" {
			var preview domain.AssignmentPreview
			preview, err = a.Svc.PreviewAssignment(id, input.Revision, input.Candidates)
			if err == nil {
				writeJSON(w, preview, http.StatusOK)
				return
			}
		} else if len(input.Candidates) > 0 {
			in, err = a.Svc.ConfirmAssignmentCandidate(id, input.Revision, input.Candidates, input.SelectedCandidateID, input.PreviewChecksum, input.Actor, input.RequestID)
		} else {
			var dueAt time.Time
			dueAt, err = time.Parse(time.RFC3339, input.DueAt)
			if err != nil {
				writeValidation(w, &domain.ValidationError{Field: "due_at", Message: "分派期限必须为 RFC3339 格式"})
				return
			}
			if input.TransferAssignee != "" {
				in, err = a.Svc.TransferAssignee(id, input.Revision, input.TransferAssignee, input.TransferReason, dueAt, input.Actor, input.RequestID)
			} else {
				var items []domain.MitigationItem
				if err = decodeRawItems(input.Items, &items); err == nil {
					in, err = a.Svc.AssignWithContext(id, input.Revision, input.Assignee, dueAt, input.Summary, items, input.Actor, input.RequestID, input.OverdueNote, input.ContinueReason)
				}
			}
		}
	case "items":
		if input.PauseReason != "" || input.ExpectedResumeAt != "" {
			resume, parseErr := time.Parse(time.RFC3339, input.ExpectedResumeAt)
			if parseErr != nil {
				a.writeErr(w, id, &domain.ValidationError{Field: "expected_resume_at", Message: "预计恢复时间必须为 RFC3339 格式"})
				return
			}
			started := time.Now().UTC()
			if input.StartedAt != "" {
				started, parseErr = time.Parse(time.RFC3339, input.StartedAt)
				if parseErr != nil {
					a.writeErr(w, id, &domain.ValidationError{Field: "started_at", Message: "开始时间必须为 RFC3339 格式"})
					return
				}
			}
			in, err = a.Svc.PauseItem(id, input.Revision, input.ItemID, input.PauseReason, started, resume, input.Actor, input.RequestID)
		} else if input.ResumedAt != "" {
			resumed, parseErr := time.Parse(time.RFC3339, input.ResumedAt)
			if parseErr != nil {
				a.writeErr(w, id, &domain.ValidationError{Field: "resumed_at", Message: "恢复时间必须为 RFC3339 格式"})
				return
			}
			in, err = a.Svc.ResumeItem(id, input.Revision, input.ItemID, resumed, input.Actor, input.RequestID)
		} else if len(input.ProcessRecordSequences) > 0 {
			in, err = a.Svc.CompleteItemWithRecords(id, input.Revision, input.ItemID, input.Note, input.EffectReadings, input.ProcessRecordSequences, input.Actor, input.RequestID)
		} else if len(input.Items) > 0 && string(input.Items) != "null" {
			var items []domain.ItemCompletion
			if err = decodeRawItems(input.Items, &items); err == nil {
				in, err = a.Svc.RecordItemsBatch(id, input.Revision, items, input.Actor, input.RequestID)
			}
		} else if input.Correction {
			in, err = a.Svc.CorrectReadings(id, input.Revision, input.ItemID, input.Note, input.CorrectionReason, input.EffectReadings, input.Actor, input.RequestID)
		} else {
			in, err = a.Svc.RecordReadings(id, input.Revision, input.ItemID, input.Note, input.EffectReadings, input.Actor, input.RequestID)
		}
	case "readings-correction", "reading-correction":
		correctionReason := input.CorrectionReason
		if correctionReason == "" {
			correctionReason = input.Reason
		}
		in, err = a.Svc.CorrectRegistrationReading(id, input.Revision, input.ReadingID, input.ReplacementReading, correctionReason, input.Actor, input.RequestID)
	case "submit-review":
		if input.ComparisonChecksum != "" {
			in, err = a.Svc.SubmitWithReviewLock(id, input.Revision, input.Actor, input.RequestID, input.ComparisonChecksum, input.ConfirmedReadingIDs)
		} else {
			in, err = a.Svc.Submit(id, input.Revision, input.Actor, input.RequestID)
		}
	case "verification":
		if len(input.MetricDecisions) > 0 {
			in, err = a.Svc.VerifyMetricsWithStandards(id, input.Revision, input.Reviewer, input.Decision, input.Reason, input.RequestID, input.MetricDecisions, input.AcceptanceStandards)
		} else {
			in, err = a.Svc.VerifyConfirmedCategory(id, input.Revision, input.Reviewer, input.Decision, input.ReturnCategory, input.Reason, input.RequestID, input.ConfirmedReadingIDs)
		}
	case "manual-review":
		in, err = a.Svc.ConfirmManualReview(id, input.Revision, input.Approve, input.Actor, input.RequestID)
	case "plan-change", "plan-changes":
		in, err = a.Svc.ChangePlan(id, input.Revision, input.PlanChange, input.Reason, input.Approver, input.RequestID)
	case "process-records", "item-records":
		if input.ProcessRecord.Type == "" && input.RecordType != "" {
			occurred, parseErr := time.Parse(time.RFC3339, input.OccurredAt)
			if parseErr != nil {
				writeValidation(w, &domain.ValidationError{Field: "occurred_at", Message: "过程发生时间必须为 RFC3339 格式"})
				return
			}
			input.ProcessRecord = domain.ProcessRecord{Type: input.RecordType, OccurredAt: occurred, Note: input.Note, Reading: input.Reading, EvidenceRef: input.EvidenceRef}
		}
		in, err = a.Svc.AddProcessRecord(id, input.Revision, input.ItemID, input.ProcessRecord, input.Actor, input.RequestID)
	case "deadline-change-request":
		var requested time.Time
		requested, err = time.Parse(time.RFC3339, input.RequestedDueAt)
		if err != nil {
			writeValidation(w, &domain.ValidationError{Field: "requested_due_at", Message: "待审批期限必须为 RFC3339 格式"})
			return
		}
		in, err = a.Svc.RequestDeadlineChange(id, input.Revision, requested, input.Reason, input.AffectedItemIDs, input.Applicant, input.RequestID)
	case "deadline-change-decision":
		if input.Decision == "批准" {
			input.Approve = true
		}
		if input.Decision != "" && input.Decision != "批准" && input.Decision != "驳回" {
			writeValidation(w, &domain.ValidationError{Field: "decision", Message: "期限变更决定只能为批准或驳回"})
			return
		}
		in, err = a.Svc.DecideDeadlineChange(id, input.Revision, input.Approve, input.Actor, input.DecisionNote, input.RequestID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if action == "plan-change" || action == "plan-changes" {
			var validation *domain.ValidationError
			if errors.As(err, &validation) {
				body := map[string]interface{}{"code": "plan_change_blocked", "error": validation.Message, "field": validation.Field}
				if len(validation.MissingMetrics) > 0 {
					body["missing_metrics"] = validation.MissingMetrics
				}
				writeJSON(w, body, http.StatusUnprocessableEntity)
				return
			}
		}
		a.writeErr(w, id, err)
		return
	}
	writeJSON(w, in, http.StatusOK)
}

func (a *API) batchAssignment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Entries          []workflow.BatchAssignmentEntry `json:"entries"`
		Incidents        []workflow.BatchAssignmentEntry `json:"incidents"`
		Assignee         string                          `json:"assignee"`
		DueAt            string                          `json:"due_at"`
		DueAfterSeconds  int64                           `json:"due_after_seconds"`
		DeadlineStrategy string                          `json:"deadline_strategy"`
		Summary          string                          `json:"summary"`
		Items            []domain.MitigationItem         `json:"items"`
		Template         []domain.MitigationItem         `json:"template"`
		Actor            string                          `json:"actor"`
		RequestID        string                          `json:"request_id"`
		BatchRequestID   string                          `json:"batch_request_id"`
		OverdueNote      string                          `json:"overdue_note"`
		Preflight        bool                            `json:"preflight"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, map[string]interface{}{"code": "invalid_json", "error": "请求格式错误", "detail": err.Error()}, http.StatusBadRequest)
		return
	}
	if len(input.Entries) == 0 {
		input.Entries = input.Incidents
	}
	if len(input.Items) == 0 {
		input.Items = input.Template
	}
	if input.RequestID == "" {
		input.RequestID = input.BatchRequestID
	}
	var due time.Time
	var err error
	if input.DueAt != "" {
		due, err = time.Parse(time.RFC3339, input.DueAt)
		if err != nil {
			writeValidation(w, &domain.ValidationError{Field: "due_at", Message: "统一期限必须为 RFC3339 格式"})
			return
		}
	}
	if input.DeadlineStrategy != "" && input.DeadlineStrategy != "recommended" && input.DeadlineStrategy != "absolute" && input.DeadlineStrategy != "relative" {
		writeValidation(w, &domain.ValidationError{Field: "deadline_strategy", Message: "期限策略只能为 recommended、absolute 或 relative"})
		return
	}
	command := workflow.BatchAssignmentCommand{Entries: input.Entries, Assignee: input.Assignee, DueAt: due, DueAfter: time.Duration(input.DueAfterSeconds) * time.Second, Summary: input.Summary, Items: input.Items, Actor: input.Actor, RequestID: input.RequestID, OverdueNote: input.OverdueNote}
	if input.Preflight {
		writeJSON(w, a.Svc.PreflightBatchAssignment(command), http.StatusOK)
		return
	}
	result, err := a.Svc.AssignBatch(command)
	if err != nil {
		a.writeErr(w, "", err)
		return
	}
	writeJSON(w, result, http.StatusOK)
}

func decodeJSON(r *http.Request, value interface{}) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func decodeRawItems(raw json.RawMessage, value interface{}) error {
	if len(raw) == 0 || string(raw) == "null" {
		return &domain.ValidationError{Field: "items", Message: "措施项不能为空"}
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return &domain.ValidationError{Field: "items", Message: "措施项格式错误: " + err.Error()}
	}
	return nil
}

func hasBaselineReading(readings []domain.EnvironmentalReading) bool {
	for _, reading := range readings {
		if reading.Phase == domain.PhaseBaseline {
			return true
		}
	}
	return false
}

func parseIncidentFilter(r *http.Request) (domain.IncidentFilter, error) {
	query := r.URL.Query()
	itemSensitivity := strings.TrimSpace(query.Get("sensitivity"))
	if alias := strings.TrimSpace(query.Get("item_sensitivity")); alias != "" {
		if itemSensitivity != "" && itemSensitivity != alias {
			return domain.IncidentFilter{}, &domain.ValidationError{Field: "sensitivity", Message: "藏品敏感级别筛选值互相冲突"}
		}
		itemSensitivity = alias
	}
	collectionID := strings.TrimSpace(query.Get("collection_id"))
	if collectionID == "" {
		collectionID = strings.TrimSpace(query.Get("affected_items.collection_id"))
	}
	filter := domain.IncidentFilter{
		Status: domain.Status(query.Get("status")), AreaID: strings.TrimSpace(query.Get("area_id")),
		RiskLevel: domain.RiskLevel(query.Get("risk_level")), DeadlineBucket: query.Get("deadline_bucket"),
		CollectionID: collectionID, Material: strings.TrimSpace(query.Get("material")), ItemSensitivity: itemSensitivity,
	}
	if query.Has("collection_id") && strings.TrimSpace(query.Get("collection_id")) == "" || query.Has("affected_items.collection_id") && strings.TrimSpace(query.Get("affected_items.collection_id")) == "" {
		return filter, &domain.ValidationError{Field: "collection_id", Message: "藏品编号筛选值不能为空"}
	}
	if len(filter.CollectionID) > 200 || strings.ContainsAny(filter.CollectionID, "\r\n\t") {
		return filter, &domain.ValidationError{Field: "collection_id", Message: "藏品编号筛选值格式非法"}
	}
	if query.Has("material") && filter.Material == "" {
		return filter, &domain.ValidationError{Field: "material", Message: "材质筛选值不能为空"}
	}
	if filter.Material != "" && !domain.IsSupportedMaterial(filter.Material) {
		return filter, &domain.ValidationError{Field: "material", Message: "材质类别不受支持"}
	}
	if query.Has("sensitivity") && strings.TrimSpace(query.Get("sensitivity")) == "" || query.Has("item_sensitivity") && strings.TrimSpace(query.Get("item_sensitivity")) == "" {
		return filter, &domain.ValidationError{Field: "sensitivity", Message: "藏品敏感级别筛选值不能为空"}
	}
	if filter.ItemSensitivity != "" && !domain.IsAffectedItemSensitivity(filter.ItemSensitivity) {
		return filter, &domain.ValidationError{Field: "sensitivity", Message: "藏品敏感级别只能为高、中或低"}
	}
	allowedStatuses := map[domain.Status]bool{"": true, domain.StatusPending: true, domain.StatusMitigating: true, domain.StatusReview: true, domain.StatusClosed: true}
	if !allowedStatuses[filter.Status] {
		return filter, &domain.ValidationError{Field: "status", Message: "事件状态筛选值不受支持"}
	}
	allowedRisks := map[domain.RiskLevel]bool{"": true, domain.RiskLow: true, domain.RiskMedium: true, domain.RiskHigh: true, domain.RiskCritical: true}
	if !allowedRisks[filter.RiskLevel] {
		return filter, &domain.ValidationError{Field: "risk_level", Message: "风险等级筛选值不受支持"}
	}
	var err error
	if value := strings.TrimSpace(query.Get("observed_from")); value != "" {
		filter.ObservedFrom, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, &domain.ValidationError{Field: "observed_from", Message: "观测起始时间必须为 RFC3339 格式"}
		}
	}
	if value := strings.TrimSpace(query.Get("observed_to")); value != "" {
		filter.ObservedTo, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return filter, &domain.ValidationError{Field: "observed_to", Message: "观测结束时间必须为 RFC3339 格式"}
		}
	}
	if !filter.ObservedFrom.IsZero() && !filter.ObservedTo.IsZero() && filter.ObservedFrom.After(filter.ObservedTo) {
		return filter, &domain.ValidationError{Field: "observed_from", Message: "观测起始时间不得晚于结束时间"}
	}
	allowedBuckets := map[string]bool{"": true, "pending_overdue": true, "mitigating_overdue": true, "due_soon": true, "retest_due": true, "retest_overdue": true}
	if !allowedBuckets[filter.DeadlineBucket] {
		return filter, &domain.ValidationError{Field: "deadline_bucket", Message: "期限桶条件不受支持"}
	}
	return filter, nil
}

func parseTimelineFilter(r *http.Request) (workflow.TimelineFilter, error) {
	query := r.URL.Query()
	filter := workflow.TimelineFilter{EventType: strings.TrimSpace(query.Get("event_type")), Actor: strings.TrimSpace(query.Get("actor"))}
	allowed := map[string]bool{"": true, "登记与研判": true, "登记读数更正": true, "基线补录": true, "补充观测": true, "风险变更": true, "分派": true, "逾期升级": true, "执行人交接": true, "方案变更": true, "措施过程记录": true, "期限变更申请": true, "期限变更决定": true, "措施完成": true, "措施批量完成": true, "执行记录更正": true, "措施暂停": true, "措施恢复": true, "待人工复核": true, "可信度确认": true, "可信度驳回": true, "提交复核": true, "退回处置": true, "关闭": true}
	if !allowed[filter.EventType] {
		return filter, &domain.ValidationError{Field: "event_type", Message: "时间线事件类型不受支持"}
	}
	var err error
	if value := query.Get("round"); value != "" {
		filter.Round, err = strconv.Atoi(value)
		if err != nil || filter.Round < 1 {
			return filter, &domain.ValidationError{Field: "round", Message: "处置轮次必须为正整数"}
		}
	}
	filter.Cursor, err = workflow.ParseTimelineCursor(query.Get("cursor"))
	if err != nil {
		return filter, err
	}
	if value := query.Get("limit"); value != "" {
		filter.Limit, err = strconv.Atoi(value)
		if err != nil || filter.Limit < 1 || filter.Limit > 100 {
			return filter, &domain.ValidationError{Field: "limit", Message: "分页数量必须为 1 到 100"}
		}
	}
	return filter, nil
}

func writeValidation(w http.ResponseWriter, validation *domain.ValidationError) {
	body := map[string]interface{}{"code": "validation_error", "error": validation.Message, "field": validation.Field}
	if len(validation.MissingMetrics) > 0 {
		body["missing_metrics"] = validation.MissingMetrics
	}
	if len(validation.Comparisons) > 0 {
		body["comparisons"] = validation.Comparisons
	}
	writeJSON(w, body, http.StatusBadRequest)
}

func (a *API) writeErr(w http.ResponseWriter, incidentID string, err error) {
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		body := map[string]interface{}{"code": "validation_error", "error": validation.Message, "field": validation.Field}
		if len(validation.MissingMetrics) > 0 {
			body["missing_metrics"] = validation.MissingMetrics
		}
		if len(validation.Comparisons) > 0 {
			body["comparisons"] = validation.Comparisons
		}
		if incidentID != "" {
			if current, e := a.Repo.Get(incidentID); e == nil {
				body["current"] = current
			}
		}
		writeJSON(w, body, http.StatusBadRequest)
		return
	}
	var candidate *domain.CandidateConflictError
	if errors.As(err, &candidate) {
		writeJSON(w, map[string]interface{}{"code": candidate.Kind, "error": candidate.Message, "candidates": candidate.Candidates}, http.StatusConflict)
		return
	}
	var batch *domain.BatchConflictError
	if errors.As(err, &batch) {
		writeJSON(w, map[string]interface{}{"code": "batch_preflight_failed", "error": batch.Error(), "results": batch.Results}, http.StatusConflict)
		return
	}
	var workload *domain.WorkloadConflictError
	if errors.As(err, &workload) {
		writeJSON(w, map[string]interface{}{"code": "assignment_workload_conflict", "error": workload.Message, "workload_snapshot": workload.Snapshot}, http.StatusConflict)
		return
	}
	var dependency *domain.DependencyBlockedError
	if errors.As(err, &dependency) {
		writeJSON(w, map[string]interface{}{"code": "dependency_blocked", "error": dependency.Error(), "item_id": dependency.ItemID, "blocked_by": dependency.BlockedBy}, http.StatusUnprocessableEntity)
		return
	}
	var integrity *domain.IntegrityError
	if errors.As(err, &integrity) {
		writeJSON(w, map[string]interface{}{"code": "data_integrity_error", "error": integrity.Message}, http.StatusInternalServerError)
		return
	}
	var idem *domain.IdempotencyConflictError
	if errors.As(err, &idem) {
		writeJSON(w, map[string]interface{}{"code": "idempotency_conflict", "error": "request_id 已用于不同操作或业务内容", "current": map[string]interface{}{"incident_id": idem.IncidentID, "status": idem.Status, "revision": idem.Revision}}, http.StatusConflict)
		return
	}
	if errors.Is(err, domain.ErrConflict) {
		body := map[string]interface{}{"code": "revision_conflict", "error": "事件修订号已变化"}
		if incidentID != "" {
			if current, e := a.Repo.Get(incidentID); e == nil {
				body["current"] = current
			}
		}
		writeJSON(w, body, http.StatusConflict)
		return
	}
	if errors.Is(err, domain.ErrNotFound) {
		writeJSON(w, map[string]interface{}{"code": "not_found", "error": "事件不存在"}, http.StatusNotFound)
		return
	}
	if errors.Is(err, domain.ErrConflict) {
		body := map[string]interface{}{"code": "revision_conflict", "error": "事件修订号已变化"}
		if current, getErr := a.Svc.Get(incidentID); getErr == nil {
			body["current"] = map[string]interface{}{"incident_id": current.ID, "status": current.Status, "revision": current.Revision}
		}
		writeJSON(w, body, http.StatusConflict)
		return
	}
	if errors.Is(err, domain.ErrState) {
		writeJSON(w, map[string]interface{}{"code": "invalid_state", "error": "当前事件状态不允许此操作"}, http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, map[string]interface{}{"code": "invalid_request", "error": err.Error()}, http.StatusBadRequest)
}

const indexHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>馆藏文物保存环境异常处置台</title><style>:root{color-scheme:light}*{box-sizing:border-box}body{font-family:system-ui,sans-serif;margin:0;background:#f4f5f3;color:#1f2522}header{background:#263a31;color:white;padding:1.2rem 5vw}h1{font-size:1.45rem;margin:0;letter-spacing:0}h2{font-size:1.08rem;letter-spacing:0}main{max-width:1180px;margin:auto;padding:1.5rem}.panel{background:white;border:1px solid #d8ddd9;padding:1rem;margin-bottom:1rem;border-radius:6px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:.75rem}label{display:grid;gap:.25rem;font-size:.88rem}input,select,textarea,button{font:inherit;padding:.55rem;border:1px solid #aeb8b2}button{background:#345b49;color:white;cursor:pointer;border-radius:4px}.secondary{background:white;color:#345b49}.stats{display:flex;gap:.5rem;flex-wrap:wrap}.stat{padding:.55rem .7rem;background:#edf1ee;color:#1f2522}.incident{border-top:1px solid #ddd;padding:.8rem 0}pre{white-space:pre-wrap;overflow:auto;font-size:.8rem;max-height:36rem}.error{color:#a12626}.actions{border-top:1px solid #d8ddd9;margin-top:1rem;padding-top:.75rem}details{margin:.6rem 0}summary{cursor:pointer;font-weight:600}@media(max-width:600px){main{padding:.75rem}}</style></head><body><header><h1>馆藏文物保存环境异常处置台</h1></header><main><section class="panel"><h2>异常登记与风险研判</h2><form id="create"><div class="grid"><label>事件编号<input name="id" required></label><label>保存区域<input name="area" required></label><label>受影响藏品范围<input name="scope" required></label><label>藏品敏感级别<select name="sensitivity"><option>高</option><option>中</option><option>低</option></select></label><label>温度异常读数<input name="temp" type="number" step="any" value="95" required></label><label>温度单位<select name="temp_unit"><option value="℉">℉</option><option value="℃">℃</option></select></label><label>温度基线读数<input name="temp_baseline" type="number" step="any"></label><label>基线提前小时数<input name="baseline_hours" type="number" min="1" value="2"></label><label>温度证据引用<input name="temp_evidence" required></label><label>温度基线证据<input name="temp_baseline_evidence"></label><label>湿度异常读数（%RH）<input name="humidity" type="number" step="any" value="70" required></label><label>湿度基线读数（%RH）<input name="humidity_baseline" type="number" step="any"></label><label>湿度证据引用<input name="humidity_evidence" required></label><label>湿度基线证据<input name="humidity_baseline_evidence"></label><label>独立登记理由<textarea name="independent_reason"></textarea></label></div><p><button id="preflight" type="button" class="secondary">登记预检</button> <button type="submit">确认登记</button></p></form><pre id="result"></pre></section><section class="panel"><h2>事件队列</h2><div class="grid"><label>状态<select id="status"><option value="">全部</option><option>待分派</option><option>处置中</option><option>待复核</option><option>已关闭</option></select></label><label>区域<input id="areaFilter"></label><label>风险<select id="risk"><option value="">全部</option><option>紧急</option><option>高</option><option>中</option><option>低</option></select></label><label>观测开始<input id="observedFrom" type="datetime-local"></label><label>观测结束<input id="observedTo" type="datetime-local"></label></div><p><button id="refresh">查询</button></p><div class="stats" id="stats"></div><div id="dimensions"></div><div id="list"></div></section><section class="panel"><h2>事件详情</h2><div class="grid"><label>时间线动作<select id="eventType"><option value="">全部</option><option>登记与研判</option><option>登记读数更正</option><option>分派</option><option>执行人交接</option><option>措施完成</option><option>措施批量完成</option><option>执行记录更正</option><option>提交复核</option><option>退回处置</option><option>关闭</option></select></label><label>操作人<input id="timelineActor"></label><label>处置轮次<input id="timelineRound" type="number" min="1"></label></div><p><button id="timelineFilter" class="secondary">筛选时间线</button> <button id="archive" class="secondary">查看归档</button></p><pre id="detail">从队列中选择事件。</pre><div class="actions"><details><summary>登记读数勘误</summary><form id="correct"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>原读数 ID<input name="reading_id" required></label><label>指标<select name="metric"><option>温度</option><option>湿度</option><option>光照</option><option>污染物</option></select></label><label>替换值<input name="value" type="number" step="any" required></label><label>单位<input name="unit" required></label><label>更正原因<input name="reason" required></label><label>证据引用<input name="evidence" required></label></div><p><button>提交勘误</button></p></form></details><details><summary>执行人交接</summary><form id="transfer"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>新执行人<input name="assignee" required></label><label>新期限<input name="due_at" type="datetime-local" required></label><label>交接原因<input name="reason" required></label><label>负责人<input name="actor" required></label></div><p><button>确认交接</button></p></form></details><details><summary>批量完成措施</summary><form id="batch"><div class="grid"><label>修订号<input name="revision" type="number" required></label><label>执行人<input name="actor" required></label><label>措施项 1 ID<input name="item1" required></label><label>措施项 1 说明<input name="note1" required></label><label>措施项 1 指标<input name="metric1" value="温度" required></label><label>措施项 1 效果值<input name="value1" type="number" step="any" required></label><label>措施项 1 单位<input name="unit1" value="℃" required></label><label>措施项 1 证据<input name="evidence1" required></label><label>措施项 2 ID<input name="item2"></label><label>措施项 2 说明<input name="note2"></label><label>措施项 2 指标<input name="metric2" value="温度"></label><label>措施项 2 效果值<input name="value2" type="number" step="any"></label><label>措施项 2 单位<input name="unit2" value="℃"></label><label>措施项 2 证据<input name="evidence2"></label></div><p><button>原子提交</button></p></form></details><pre id="actionResult"></pre></div></section></main><script src="/static/app.js"></script></body></html>`

const appJS = `const q=s=>document.querySelector(s);let bucket='',selected='';const iso=v=>v?new Date(v).toISOString():'';async function request(path,body){const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}),d=await r.json();q('#actionResult').className=r.ok?'':'error';q('#actionResult').textContent=JSON.stringify(d,null,2);if(r.ok){load();detail(selected)}return d}async function load(){const p=new URLSearchParams({status:q('#status').value,area_id:q('#areaFilter').value,risk_level:q('#risk').value,deadline_bucket:bucket,observed_from:iso(q('#observedFrom').value),observed_to:iso(q('#observedTo').value)});const r=await fetch('/api/incidents?'+p),d=await r.json();if(!r.ok){q('#list').innerHTML='<p class="error">'+d.error+'</p>';return}const s=d.statistics,buttons=[['全部 '+s.total,''],['待分派逾期 '+s.pending_overdue,'pending_overdue'],['处置逾期 '+s.mitigating_overdue,'mitigating_overdue'],['即将到期 '+s.due_soon,'due_soon']];q('#stats').innerHTML=buttons.map(x=>'<button class="stat" data-bucket="'+x[1]+'">'+x[0]+'</button>').join('');q('#dimensions').textContent=s.status_dimensions.map(x=>x.key+' '+x.count).join(' · ');q('#stats').querySelectorAll('button').forEach(b=>b.onclick=()=>{bucket=b.dataset.bucket;load()});q('#list').innerHTML=d.incidents.map(i=>'<article class="incident"><input class="batchPick" type="checkbox" value="'+i.id+'" data-revision="'+i.revision+'" '+(i.status==='待分派'?'':'disabled')+'> <button data-id="'+i.id+'">查看</button> <b>'+i.id+'</b>　'+i.status+' · '+i.risk_level+' · '+i.area_id+'　修订号 '+i.revision+(i.deadline.overdue?'　<span class="error">已逾期</span>':'')+'</article>').join('');q('#list').querySelectorAll('button').forEach(b=>b.onclick=()=>{selected=b.dataset.id;detail(selected)})}async function detail(id,archive=false){if(!id)return;const p=archive?new URLSearchParams({view:'archive'}):new URLSearchParams({event_type:q('#eventType').value,actor:q('#timelineActor').value,round:q('#timelineRound').value}),r=await fetch('/api/incidents/'+encodeURIComponent(id)+'?'+p);q('#detail').textContent=JSON.stringify(await r.json(),null,2)}function createBody(preflight){const f=new FormData(q('#create')),now=new Date(),at=now.toISOString(),base=new Date(now-Number(f.get('baseline_hours'))*3600000).toISOString(),id=f.get('id'),readings=[];const add=(suffix,phase,metric,value,unit,evidence,measured)=>{if(value==='')return;readings.push({id:id+'-'+suffix,phase,metric,value:Number(value),unit,measured_at:measured,source_note:phase==='baseline'?'历史监测基线':'现场监测仪',evidence_ref:evidence,evidence_recorded_at:measured})};add('tb','baseline','温度',f.get('temp_baseline'),f.get('temp_unit'),f.get('temp_baseline_evidence'),base);add('t','abnormal','温度',f.get('temp'),f.get('temp_unit'),f.get('temp_evidence'),at);add('hb','baseline','湿度',f.get('humidity_baseline'),'%RH',f.get('humidity_baseline_evidence'),base);add('h','abnormal','湿度',f.get('humidity'),'%RH',f.get('humidity_evidence'),at);const affected_items=[...document.querySelectorAll('.affected-row')].map(row=>({collection_id:row.querySelector('[name=collection_id]').value,material:row.querySelector('[name=material]').value,quantity:Number(row.querySelector('[name=quantity]').value),sensitivity:row.querySelector('[name=item_sensitivity]').value,impact_note:row.querySelector('[name=impact_note]').value})).filter(x=>x.collection_id);return{id,area_id:f.get('area'),affected_scope:f.get('scope'),affected_items,sensitivity:f.get('sensitivity'),actor:'保管员',request_id:crypto.randomUUID(),independent_reason:f.get('independent_reason'),observed_at:at,readings,preflight}}async function submitCreate(preflight){const body=createBody(preflight),r=await fetch('/api/incidents',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}),d=await r.json();q('#result').className=r.ok&&d.valid!==false?'':'error';q('#result').textContent=JSON.stringify(d,null,2);if(r.status===201){load();selected=body.id;detail(selected)}}q('#correct').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target),now=new Date().toISOString();request('/api/incidents/'+encodeURIComponent(selected)+'/readings-correction',{revision:Number(f.get('revision')),reading_id:f.get('reading_id'),replacement_reading:{metric:f.get('metric'),value:Number(f.get('value')),unit:f.get('unit'),measured_at:now,source_note:'勘误复核',evidence_ref:f.get('evidence'),evidence_recorded_at:now},reason:f.get('reason'),actor:'保管员',request_id:crypto.randomUUID()})};q('#transfer').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);request('/api/incidents/'+encodeURIComponent(selected)+'/assignment',{revision:Number(f.get('revision')),transfer_assignee:f.get('assignee'),transfer_reason:f.get('reason'),due_at:iso(f.get('due_at')),actor:f.get('actor'),request_id:crypto.randomUUID()})};q('#batch').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target),now=new Date().toISOString(),items=[1,2].filter(n=>f.get('item'+n)).map(n=>({item_id:f.get('item'+n),note:f.get('note'+n),effect_readings:[{metric:f.get('metric'+n),value:Number(f.get('value'+n)),unit:f.get('unit'+n),measured_at:now,source_note:'现场复测',evidence_ref:f.get('evidence'+n),evidence_recorded_at:now}]}));request('/api/incidents/'+encodeURIComponent(selected)+'/items',{revision:Number(f.get('revision')),items,actor:f.get('actor'),request_id:crypto.randomUUID()})};q('#refresh').onclick=()=>{bucket='';load()};q('#timelineFilter').onclick=()=>detail(selected);q('#archive').onclick=()=>detail(selected,true);q('#preflight').onclick=()=>submitCreate(true);q('#create').onsubmit=e=>{e.preventDefault();submitCreate(false)};load();`
