package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
	Version       int    `json:"version"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type progressView struct {
	ID       string `json:"id"`
	Sequence int    `json:"sequence"`
	Percent  int    `json:"percent"`
}

type errorView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "version-guard":
		return versionGuard(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

// versionGuard 验证写请求可以携带已读取的版本参与成功条件：携带匹配版本的
// 提交按序推进标本与方案版本，携带陈旧版本的提交得到明确冲突且不产生写入，
// 每一步都通过标本详情、方案快照和写入响应核对同一次提交的版本事实。
func versionGuard(client *http.Client, baseURL string) error {
	var specimen specimenView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-002",
			FieldLabel:    "南沟-12",
			TaxonHint:     "小型爬行动物",
			MatrixKind:    "泥岩",
			JacketNotes:   "围岩松散",
		},
		http.StatusCreated,
		&specimen,
	); err != nil {
		return err
	}
	if specimen.Version != 1 {
		return fmt.Errorf("expected specimen version 1 after registration, got %d", specimen.Version)
	}

	// 携带已读取的标本版本创建方案，标本版本同步推进。
	var plan planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		contracts.CreatePlanRequest{
			Objective:       "清理围岩暴露骨骼",
			Method:          "气动笔剥离",
			Adhesive:        "可逆树脂",
			TargetExposure:  60,
			ExpectedVersion: versionPtr(1),
		},
		http.StatusCreated,
		&plan,
	); err != nil {
		return err
	}
	if plan.Version != 1 {
		return fmt.Errorf("expected plan version 1 after creation, got %d", plan.Version)
	}
	if err := expectSpecimenState(client, baseURL, specimen.ID, "planned", 2); err != nil {
		return err
	}

	// 基于过期标本版本的创建必须得到明确冲突。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		contracts.CreatePlanRequest{
			Objective:       "重复清理围岩",
			Method:          "气动笔剥离",
			TargetExposure:  60,
			ExpectedVersion: versionPtr(1),
		},
		http.StatusConflict,
		"specimen_version_conflict",
	); err != nil {
		return err
	}

	// 携带已读取的方案版本审批，方案快照与标本详情呈现同一次提交的版本事实。
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: versionPtr(1)},
		http.StatusOK,
		&approved,
	); err != nil {
		return err
	}
	if approved.Version != 2 {
		return fmt.Errorf("expected plan version 2 after approval, got %d", approved.Version)
	}
	if err := expectPlanState(client, baseURL, plan.ID, "approved", 2, 0); err != nil {
		return err
	}
	if err := expectSpecimenState(client, baseURL, specimen.ID, "preparing", 3); err != nil {
		return err
	}

	// 过期方案版本与非正整数版本都必须被拒绝。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: versionPtr(1)},
		http.StatusConflict,
		"plan_version_conflict",
	); err != nil {
		return err
	}
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: versionPtr(0)},
		http.StatusBadRequest,
		"invalid_expected_version",
	); err != nil {
		return err
	}

	// 携带已读取的方案版本记录进度。
	var first progressView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/progress",
		contracts.ProgressRequest{
			Stage:           "基质剥离",
			Percent:         40,
			Note:            "版本链路检查",
			RecordedBy:      "preparator-chen",
			ExpectedVersion: versionPtr(2),
		},
		http.StatusCreated,
		&first,
	); err != nil {
		return err
	}
	if first.Sequence != 1 {
		return fmt.Errorf("expected first progress sequence 1, got %d", first.Sequence)
	}
	if err := expectPlanState(client, baseURL, plan.ID, "active", 3, 1); err != nil {
		return err
	}

	// 过期方案版本的进度必须冲突且不得写入。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/progress",
		contracts.ProgressRequest{
			Stage:           "重复记录",
			Percent:         60,
			RecordedBy:      "preparator-chen",
			ExpectedVersion: versionPtr(2),
		},
		http.StatusConflict,
		"plan_version_conflict",
	); err != nil {
		return err
	}
	if err := expectPlanState(client, baseURL, plan.ID, "active", 3, 1); err != nil {
		return err
	}

	// 基于最新版本完成制备，方案与标本在同一事务中进入终态。
	var final progressView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/progress",
		contracts.ProgressRequest{
			Stage:           "表面加固",
			Percent:         100,
			Note:            "版本链路检查",
			RecordedBy:      "preparator-chen",
			ExpectedVersion: versionPtr(3),
		},
		http.StatusCreated,
		&final,
	); err != nil {
		return err
	}
	if final.Sequence != 2 {
		return fmt.Errorf("expected final progress sequence 2, got %d", final.Sequence)
	}
	if err := expectPlanState(client, baseURL, plan.ID, "completed", 4, 2); err != nil {
		return err
	}
	if err := expectSpecimenState(client, baseURL, specimen.ID, "stabilized", 4); err != nil {
		return err
	}

	// 携带已读取的标本版本办理放行。
	var release releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:         "比较解剖研究",
			Recipient:       "地层研究组",
			ApprovedBy:      "keeper-zhou",
			ExpectedVersion: versionPtr(4),
		},
		http.StatusCreated,
		&release,
	); err != nil {
		return err
	}
	if release.SpecimenID != specimen.ID {
		return errors.New("release record references the wrong specimen")
	}
	if err := expectSpecimenState(client, baseURL, specimen.ID, "available", 5); err != nil {
		return err
	}

	// 过期标本版本的放行必须冲突；未携带版本时仍由放行唯一性拦截。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:         "重复放行",
			Recipient:       "地层研究组",
			ApprovedBy:      "keeper-zhou",
			ExpectedVersion: versionPtr(4),
		},
		http.StatusConflict,
		"specimen_version_conflict",
	); err != nil {
		return err
	}
	return expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "重复放行",
			Recipient:  "地层研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusConflict,
		"release_exists",
	)
}

func versionPtr(value int) *int {
	return &value
}

func expectSpecimenState(client *http.Client, baseURL, specimenID, status string, version int) error {
	var fetched specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimenID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != status {
		return fmt.Errorf("expected specimen status %s, got %s", status, fetched.Status)
	}
	if fetched.Version != version {
		return fmt.Errorf("expected specimen version %d, got %d", version, fetched.Version)
	}
	return nil
}

func expectPlanState(client *http.Client, baseURL, planID, status string, version int, progressCount int) error {
	var snapshot planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+planID, nil, http.StatusOK, &snapshot); err != nil {
		return err
	}
	if snapshot.Plan.Status != status {
		return fmt.Errorf("expected plan status %s, got %s", status, snapshot.Plan.Status)
	}
	if snapshot.Plan.Version != version {
		return fmt.Errorf("expected plan version %d, got %d", version, snapshot.Plan.Version)
	}
	if len(snapshot.Progress) != progressCount {
		return fmt.Errorf("expected %d progress entries, got %d", progressCount, len(snapshot.Progress))
	}
	return nil
}

func expectError(client *http.Client, method, url string, body any, wantStatus int, wantCode string) error {
	var failure errorView
	if err := requestJSON(client, method, url, body, wantStatus, &failure); err != nil {
		return err
	}
	if failure.Code != wantCode {
		return fmt.Errorf("expected error code %s, got %s", wantCode, failure.Code)
	}
	return nil
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-001",
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
