package station

import "example.com/paleo-prep-station/internal/domain"

// 写入结果在原实体之外附带“本次提交的版本事实”：调用方携带的已读版本、
// 提交命中的提交前版本以及写后版本。这样一次成功提交可以被明确判断为
// “基于自己看到的事实”，而不是恰好覆盖了别人推进过的状态。
//
// 实体字段保持内联 JSON 输出，未携带版本的既有调用方仍读取到原对象形状。

// CreatePlanResult 返回新建方案，以及它所依据与推进的标本版本。
type CreatePlanResult struct {
	domain.PrepPlan
	ExpectedVersion *int `json:"expected_version,omitempty"`
	SpecimenVersion int  `json:"specimen_version"`
}

// ApprovalResult 返回更新后的方案，并同时给出方案与标本两侧的版本事实，
// 因为审批在同一事务中推进了这两个聚合。
type ApprovalResult struct {
	domain.PrepPlan
	ExpectedVersion *int `json:"expected_version,omitempty"`
	PlanVersion     int  `json:"plan_version"`
	SpecimenVersion int  `json:"specimen_version"`
}

// ProgressResult 返回新增进度，并给出方案与标本（可能未变）的版本事实。
type ProgressResult struct {
	domain.ProgressEntry
	ExpectedVersion *int `json:"expected_version,omitempty"`
	PlanVersion     int  `json:"plan_version"`
	SpecimenVersion int  `json:"specimen_version"`
}

// ReleaseResult 返回放行记录与标本的版本事实。
type ReleaseResult struct {
	domain.ReleaseRecord
	ExpectedVersion *int `json:"expected_version,omitempty"`
	SpecimenVersion int  `json:"specimen_version"`
}
