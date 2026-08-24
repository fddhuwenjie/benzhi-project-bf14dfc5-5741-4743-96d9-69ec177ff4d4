package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/httpapi"
	"museum-preservation/internal/store"
	"museum-preservation/internal/workflow"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19081", "监听地址")
	self := flag.Bool("self-check", false, "运行自检")
	flag.Parse()
	if env := os.Getenv("PORT"); env != "" && flag.Lookup("addr").Value.String() == "127.0.0.1:19081" {
		*addr = "127.0.0.1:" + env
	}
	if !strings.HasPrefix(*addr, "127.0.0.1:") {
		fmt.Println("地址必须绑定回环接口")
		os.Exit(2)
	}
	if *self {
		if err := selfCheck(*addr); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}
	dir := filepath.Join(os.TempDir(), "museum-preservation-data")
	s, e := store.Open(dir)
	if e != nil {
		panic(e)
	}
	svc := &workflow.Service{Repo: s, Rules: assessment.DefaultRules()}
	api := httpapi.New(svc, s)
	srv := &http.Server{Addr: *addr, Handler: api.Mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Println("服务监听 " + *addr)
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		panic(e)
	}
}
func selfCheck(addr string) error {
	dir, e := os.MkdirTemp("", "museum-check-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	s, e := store.Open(dir)
	if e != nil {
		return e
	}
	svc := &workflow.Service{Repo: s, Rules: assessment.DefaultRules()}
	api := httpapi.New(svc, s)
	srv := &http.Server{Addr: addr, Handler: api.Mux}
	go srv.ListenAndServe()
	defer srv.Close()
	time.Sleep(80 * time.Millisecond)
	base := "http://" + addr
	now := time.Now().UTC().Format(time.RFC3339)
	post := func(path, v string) (int, error) {
		resp, e := http.Post(base+path, "application/json", strings.NewReader(v))
		if e != nil {
			return 0, e
		}
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		return resp.StatusCode, nil
	}
	body := fmt.Sprintf(`{"id":"check-1","area_id":"库房A","affected_scope":"青铜器","sensitivity":"高","actor":"保管员","request_id":"r1","observed_at":%q,"readings":[{"id":"t","phase":"abnormal","metric":"温度","value":35,"unit":"℃","measured_at":%q,"source_note":"温度记录仪","evidence_ref":"check-photo-t","evidence_recorded_at":%q}]}`, now, now, now)
	if c, e := post("/api/incidents", body); e != nil || c != 201 {
		return fmt.Errorf("登记失败 %d %v", c, e)
	}
	due := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	assign := fmt.Sprintf(`{"revision":1,"assignee":"执行人","due_at":%q,"summary":"降温","items":[{"id":"item-1","description":"开启空调","status":"待执行"}],"actor":"负责人","request_id":"r2"}`, due)
	if c, e := post("/api/incidents/check-1/assignment", assign); e != nil || c != 200 {
		return fmt.Errorf("分派失败 %d %v", c, e)
	}
	effectAt := time.Now().UTC().Format(time.RFC3339Nano)
	effect := fmt.Sprintf(`{"revision":2,"item_id":"item-1","note":"已开启空调并复测","effect_readings":[{"id":"effect-t","metric":"温度","value":22,"unit":"℃","measured_at":%q,"source_note":"复测温度计","evidence_ref":"check-photo-effect-t","evidence_recorded_at":%q}],"actor":"执行人","request_id":"r3"}`, effectAt, effectAt)
	if c, e := post("/api/incidents/check-1/items", effect); e != nil || c != 200 {
		return fmt.Errorf("执行失败 %d %v", c, e)
	}
	if c, e := post("/api/incidents/check-1/submit-review", `{"revision":3,"actor":"执行人","request_id":"r4"}`); e != nil || c != 200 {
		return fmt.Errorf("提交复核失败 %d %v", c, e)
	}
	if c, e := post("/api/incidents/check-1/verification", `{"revision":4,"reviewer":"复核人","decision":"合格","reason":"读数恢复","confirmed_reading_ids":["t","effect-t"],"request_id":"r5"}`); e != nil || c != 200 {
		return fmt.Errorf("复核失败 %d %v", c, e)
	}
	resp, e := http.Get(base + "/api/incidents/check-1?view=archive")
	if e != nil {
		return fmt.Errorf("归档查询失败: %v", e)
	}
	defer resp.Body.Close()
	var archive struct {
		Checksum       string `json:"checksum"`
		ChecksumStatus string `json:"checksum_status"`
	}
	if e = json.NewDecoder(resp.Body).Decode(&archive); e != nil || resp.StatusCode != http.StatusOK || archive.Checksum == "" || archive.ChecksumStatus != "有效" {
		return fmt.Errorf("归档校验失败 %d %#v %v", resp.StatusCode, archive, e)
	}
	fmt.Println("self-check passed")
	return nil
}
