package contracts

type RegisterSpecimenRequest struct {
	CatalogNumber string `json:"catalog_number"`
	FieldLabel    string `json:"field_label"`
	TaxonHint     string `json:"taxon_hint"`
	MatrixKind    string `json:"matrix_kind"`
	JacketNotes   string `json:"jacket_notes"`
}

type CreatePlanRequest struct {
	Objective      string `json:"objective"`
	Method         string `json:"method"`
	Adhesive       string `json:"adhesive"`
	TargetExposure int    `json:"target_exposure"`
	// ExpectedVersion 携带调用方已读取的标本版本；为空时不做版本校验。
	ExpectedVersion *int `json:"expected_version,omitempty"`
}

type ApprovalRequest struct {
	ApprovedBy string `json:"approved_by"`
	// ExpectedVersion 携带调用方已读取的方案版本；为空时不做版本校验。
	ExpectedVersion *int `json:"expected_version,omitempty"`
}

type ProgressRequest struct {
	Stage      string `json:"stage"`
	Percent    int    `json:"percent"`
	Note       string `json:"note"`
	RecordedBy string `json:"recorded_by"`
	// ExpectedVersion 携带调用方已读取的方案版本；为空时不做版本校验。
	ExpectedVersion *int `json:"expected_version,omitempty"`
}

type ReleaseRequest struct {
	Purpose    string `json:"purpose"`
	Recipient  string `json:"recipient"`
	ApprovedBy string `json:"approved_by"`
	// ExpectedVersion 携带调用方已读取的标本版本；为空时不做版本校验。
	ExpectedVersion *int `json:"expected_version,omitempty"`
}
