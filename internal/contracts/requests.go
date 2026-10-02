package contracts

// 指针语义用于区分“未携带版本（保持兼容）”与“显式携带版本”：
// 为 nil 时不参与版本成功条件；非 nil 时必须与服务端最新版本一致，
// 否则写入得到明确的 version_conflict 冲突。

type RegisterSpecimenRequest struct {
	CatalogNumber string `json:"catalog_number"`
	FieldLabel    string `json:"field_label"`
	TaxonHint     string `json:"taxon_hint"`
	MatrixKind    string `json:"matrix_kind"`
	JacketNotes   string `json:"jacket_notes"`
}

type CreatePlanRequest struct {
	Objective       string `json:"objective"`
	Method          string `json:"method"`
	Adhesive        string `json:"adhesive"`
	TargetExposure  int    `json:"target_exposure"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
}

type ApprovalRequest struct {
	ApprovedBy      string `json:"approved_by"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
}

type ProgressRequest struct {
	Stage           string `json:"stage"`
	Percent         int    `json:"percent"`
	Note            string `json:"note"`
	RecordedBy      string `json:"recorded_by"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
}

type ReleaseRequest struct {
	Purpose         string `json:"purpose"`
	Recipient       string `json:"recipient"`
	ApprovedBy      string `json:"approved_by"`
	ExpectedVersion *int   `json:"expected_version,omitempty"`
}
