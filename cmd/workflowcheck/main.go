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
	Specimen struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int    `json:"version"`
	} `json:"specimen"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

// versionFactView 承载写入响应中用于核对“同一次提交版本事实”的字段。
type versionFactView struct {
	ExpectedVersion *int `json:"expected_version,omitempty"`
	PlanVersion     int  `json:"plan_version,omitempty"`
	SpecimenVersion int  `json:"specimen_version,omitempty"`
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

	case "optimistic-version-conflict":
		return checkOptimisticVersionConflict(client, server.URL)

	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

// checkOptimisticVersionConflict 通过真实 HTTP 路由验证：
//   - 携带当前版本的创建方案、审批、进度与放行全部成功；
//   - 陈旧版本在每个写入点都得到明确的 version_conflict（409）；
//   - 非法的期望版本得到 invalid_expected_version（400）；
//   - 写入响应、标本详情与方案快照中的版本事实彼此一致；
//   - 终态与开放方案互斥等既有不变量在版本条件下仍然成立。
func checkOptimisticVersionConflict(client *http.Client, baseURL string) error {
	specimen, err := registerSpecimenAs(client, baseURL, "PPS-2026-OCC")
	if err != nil {
		return err
	}
	if specimen.Version != 1 {
		return fmt.Errorf("new specimen expected version 1, got %d", specimen.Version)
	}

	// 陈旧版本创建方案：标本当前版本为 1，携带 0 之外的过期版本 0 非法；
	// 先验证非法值与冲突分别归类。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		versionedCreatePlan(0),
		http.StatusBadRequest,
		"invalid_expected_version",
	); err != nil {
		return fmt.Errorf("invalid create-plan version: %w", err)
	}
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		versionedCreatePlan(7),
		http.StatusConflict,
		"version_conflict",
	); err != nil {
		return fmt.Errorf("stale create-plan version: %w", err)
	}

	// 携带正确版本创建方案：标本 1 -> 2，方案以版本 1 建立。
	var created struct {
		planView
		versionFactView
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		versionedCreatePlan(1),
		http.StatusCreated,
		&created,
	); err != nil {
		return err
	}
	if created.Version != 1 {
		return fmt.Errorf("new plan expected version 1, got %d", created.Version)
	}
	if created.ExpectedVersion == nil || *created.ExpectedVersion != 1 {
		return errors.New("create-plan response missing submitted expected version")
	}
	if created.SpecimenVersion != 2 {
		return fmt.Errorf("create-plan response expected specimen version 2, got %d", created.SpecimenVersion)
	}

	// 标本详情必须反映同一次提交的版本事实。
	var specimenAfterCreate specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &specimenAfterCreate); err != nil {
		return err
	}
	if specimenAfterCreate.Version != 2 || specimenAfterCreate.Status != "planned" {
		return fmt.Errorf("specimen after create-plan mismatch: status=%s version=%d", specimenAfterCreate.Status, specimenAfterCreate.Version)
	}

	// 开放方案互斥：在已推进的版本事实上携带正确版本也不能再开方案。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		versionedCreatePlan(2),
		http.StatusConflict,
		"open_plan_exists",
	); err != nil {
		return fmt.Errorf("open plan mutex not enforced: %w", err)
	}

	// 陈旧版本审批：方案已被自己读到为版本 1，但用过期版本 0（非法）和
	// 虚构版本 9 分别验证；再用正确版本 1 成功。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: ptrInt(9)},
		http.StatusConflict,
		"version_conflict",
	); err != nil {
		return fmt.Errorf("stale approval version: %w", err)
	}
	var approved struct {
		planView
		versionFactView
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: ptrInt(1)},
		http.StatusOK,
		&approved,
	); err != nil {
		return err
	}
	if approved.Version != 2 {
		return fmt.Errorf("approved plan expected version 2, got %d", approved.Version)
	}
	if approved.PlanVersion != 2 || approved.SpecimenVersion != 3 {
		return fmt.Errorf("approval version facts mismatch: plan=%d specimen=%d", approved.PlanVersion, approved.SpecimenVersion)
	}

	// 终态不可回退：再次审批即使携带“曾经正确”的版本 1 也必须冲突；
	// 携带当前版本 2 则得到 plan_not_approvable 的状态冲突，而非成功回退。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: ptrInt(1)},
		http.StatusConflict,
		"version_conflict",
	); err != nil {
		return fmt.Errorf("re-approval with stale version not rejected: %w", err)
	}
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin", ExpectedVersion: ptrInt(2)},
		http.StatusConflict,
		"plan_not_approvable",
	); err != nil {
		return fmt.Errorf("terminal plan re-approval not rejected: %w", err)
	}

	// 进度：两位制备人先后读取版本 2。第一条携带版本 2 成功，方案推进；
	// 第二条仍携带版本 2 必须得到明确冲突，不能静默覆盖或追加。
	firstProgress := versionedProgress(40, "矩阵剥离", 2)
	var progress1 struct {
		PlanID string `json:"plan_id"`
		versionFactView
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/progress",
		firstProgress,
		http.StatusCreated,
		&progress1,
	); err != nil {
		return err
	}
	if progress1.PlanVersion != 3 {
		return fmt.Errorf("active plan after first progress expected version 3, got %d", progress1.PlanVersion)
	}
	if progress1.SpecimenVersion != 3 {
		return fmt.Errorf("specimen version should stay 3 during non-final progress, got %d", progress1.SpecimenVersion)
	}

	// 第二位制备人的陈旧提交：比例更高也必须先被告知版本冲突。
	staleProgress := versionedProgress(60, "围岩清除", 2)
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/progress",
		staleProgress,
		http.StatusConflict,
		"version_conflict",
	); err != nil {
		return fmt.Errorf("stale progress version not rejected: %w", err)
	}

	// 重新读取方案快照后再提交：快照中的方案版本必须是再次提交的依据，
	// 且快照内标本版本与标本详情一致。
	var snapshotBefore planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+created.ID, nil, http.StatusOK, &snapshotBefore); err != nil {
		return err
	}
	if snapshotBefore.Plan.Version != progress1.PlanVersion {
		return fmt.Errorf("snapshot plan version %d does not match write response %d", snapshotBefore.Plan.Version, progress1.PlanVersion)
	}
	if snapshotBefore.Specimen.Version != 3 || snapshotBefore.Specimen.ID != specimen.ID {
		return fmt.Errorf("snapshot specimen fact mismatch: %+v", snapshotBefore.Specimen)
	}

	// 进度单调：基于最新版本提交回退比例 20 必须得到 progress_regressed。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/progress",
		versionedProgress(20, "回退操作", snapshotBefore.Plan.Version),
		http.StatusConflict,
		"progress_regressed",
	); err != nil {
		return fmt.Errorf("monotonic progress not enforced: %w", err)
	}

	// 终态：携带快照版本提交 100%，方案完成、标本稳定，版本事实跨视图一致。
	var progressFinal struct {
		versionFactView
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/progress",
		versionedProgress(100, "表面加固", snapshotBefore.Plan.Version),
		http.StatusCreated,
		&progressFinal,
	); err != nil {
		return err
	}
	if progressFinal.PlanVersion != 4 || progressFinal.SpecimenVersion != 4 {
		return fmt.Errorf("final progress version facts mismatch: plan=%d specimen=%d", progressFinal.PlanVersion, progressFinal.SpecimenVersion)
	}
	var stabilizedSpecimen specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &stabilizedSpecimen); err != nil {
		return err
	}
	if stabilizedSpecimen.Status != "stabilized" || stabilizedSpecimen.Version != 4 {
		return fmt.Errorf("stabilized specimen mismatch: status=%s version=%d", stabilizedSpecimen.Status, stabilizedSpecimen.Version)
	}
	var snapshotAfter planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+created.ID, nil, http.StatusOK, &snapshotAfter); err != nil {
		return err
	}
	if snapshotAfter.Plan.Status != "completed" || snapshotAfter.Plan.Version != progressFinal.PlanVersion {
		return fmt.Errorf("final snapshot plan fact mismatch: %+v", snapshotAfter.Plan)
	}
	if snapshotAfter.Specimen.Status != "stabilized" || snapshotAfter.Specimen.Version != progressFinal.SpecimenVersion {
		return fmt.Errorf("final snapshot specimen fact mismatch: %+v", snapshotAfter.Specimen)
	}
	if len(snapshotAfter.Progress) != 2 {
		return fmt.Errorf("expected exactly two progress entries, got %d", len(snapshotAfter.Progress))
	}

	// 终态不可回退：完成后携带最新方案版本也不能再追加进度。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/plans/"+created.ID+"/progress",
		versionedProgress(100, "重复收尾", snapshotAfter.Plan.Version),
		http.StatusConflict,
		"plan_not_recordable",
	); err != nil {
		return fmt.Errorf("completed plan accepted more progress: %w", err)
	}

	// 放行唯一性：陈旧标本版本 3 必须先冲突；当前版本 4 成功，标本 4 -> 5。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		versionedRelease(3),
		http.StatusConflict,
		"version_conflict",
	); err != nil {
		return fmt.Errorf("stale release version not rejected: %w", err)
	}
	var release1 struct {
		releaseView
		versionFactView
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		versionedRelease(4),
		http.StatusCreated,
		&release1,
	); err != nil {
		return err
	}
	if release1.SpecimenVersion != 5 {
		return fmt.Errorf("released specimen expected version 5, got %d", release1.SpecimenVersion)
	}
	if release1.ExpectedVersion == nil || *release1.ExpectedVersion != 4 {
		return errors.New("release response missing submitted expected version")
	}
	var availableSpecimen specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &availableSpecimen); err != nil {
		return err
	}
	if availableSpecimen.Status != "available" || availableSpecimen.Version != 5 {
		return fmt.Errorf("available specimen mismatch: status=%s version=%d", availableSpecimen.Status, availableSpecimen.Version)
	}

	// 第二次放行：携带当前版本 5 也必须被唯一性约束拒绝，不能覆盖首条记录。
	if err := expectError(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		versionedRelease(5),
		http.StatusConflict,
		"release_exists",
	); err != nil {
		return fmt.Errorf("release uniqueness not enforced: %w", err)
	}

	return nil
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	return registerSpecimenAs(client, baseURL, "PPS-2026-001")
}

func registerSpecimenAs(client *http.Client, baseURL, catalog string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: catalog,
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

func versionedCreatePlan(expected int) contracts.CreatePlanRequest {
	return contracts.CreatePlanRequest{
		Objective:       "暴露主要关节面",
		Method:          "手工显微剥离",
		Adhesive:        "可逆树脂",
		TargetExposure:  80,
		ExpectedVersion: ptrInt(expected),
	}
}

func versionedProgress(percent int, stage string, expected int) contracts.ProgressRequest {
	return contracts.ProgressRequest{
		Stage:           stage,
		Percent:         percent,
		Note:            "乐观版本链路检查",
		RecordedBy:      "preparator-chen",
		ExpectedVersion: ptrInt(expected),
	}
}

func versionedRelease(expected int) contracts.ReleaseRequest {
	return contracts.ReleaseRequest{
		Purpose:         "比较解剖研究",
		Recipient:       "地层研究组",
		ApprovedBy:      "keeper-zhou",
		ExpectedVersion: ptrInt(expected),
	}
}

func ptrInt(value int) *int {
	return &value
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

func expectError(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	wantCode string,
) error {
	var failure errorView
	if err := requestJSON(client, method, url, body, wantStatus, &failure); err != nil {
		return err
	}
	if failure.Code != wantCode {
		return fmt.Errorf("expected error code %s, got %s (%s)", wantCode, failure.Code, failure.Message)
	}
	return nil
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
