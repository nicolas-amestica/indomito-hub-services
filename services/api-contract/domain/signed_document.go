package domain

const (
	SignedUploadSKPrefix   = "SIGNED_UPLOAD#"
	SignedDocumentSKPrefix = "SIGNED_DOCUMENT#"
)

type SignedUploadIntent struct {
	PK                       string `dynamodbav:"pk"`
	SK                       string `dynamodbav:"sk"`
	Entity                   string `dynamodbav:"entity"`
	ContractID               string `dynamodbav:"contractId"`
	ID                       string `dynamodbav:"id"`
	ObjectKey                string `dynamodbav:"objectKey"`
	ExpectedSize             int64  `dynamodbav:"expectedSize"`
	ExpectedSHA256           string `dynamodbav:"expectedSha256"`
	ApprovedVersion          int    `dynamodbav:"approvedVersion"`
	ApprovedPDFSHA256        string `dynamodbav:"approvedPdfSha256"`
	ExpectedActiveDocumentID string `dynamodbav:"expectedActiveDocumentId,omitempty"`
	ReplacementReason        string `dynamodbav:"replacementReason,omitempty"`
	Status                   string `dynamodbav:"status"`
	CreatedAt                string `dynamodbav:"createdAt"`
	CreatedBy                string `dynamodbav:"createdBy"`
	CompletedAt              string `dynamodbav:"completedAt,omitempty"`
}

type SignedDocumentItem struct {
	PK         string `dynamodbav:"pk"`
	SK         string `dynamodbav:"sk"`
	Entity     string `dynamodbav:"entity"`
	ContractID string `dynamodbav:"contractId"`
	SignedDocument
}

func SignedUploadSK(id string) string   { return SignedUploadSKPrefix + id }
func SignedDocumentSK(id string) string { return SignedDocumentSKPrefix + id }
