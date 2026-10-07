package domain

import "testing"

func TestEffectiveSignatureStatusPreservesHistoricalContracts(t *testing.T) {
	tests := []struct {
		name     string
		contract Contract
		want     SignatureStatus
	}{
		{name: "draft", contract: Contract{Status: StatusDraft}, want: SignatureNotRequired},
		{name: "historical approved", contract: Contract{Status: StatusApproved}, want: SignaturePending},
		{name: "approved with stale status", contract: Contract{Status: StatusApproved, SignatureStatus: SignatureUploaded}, want: SignaturePending},
		{name: "signed", contract: Contract{Status: StatusApproved, SignedDocument: &SignedDocument{ID: "document-1"}}, want: SignatureUploaded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.contract.EffectiveSignatureStatus(); got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSignedDocumentKeysSupportDirectLookupWithoutIndex(t *testing.T) {
	if got, want := SignedUploadSK("request-1"), "SIGNED_UPLOAD#request-1"; got != want {
		t.Fatalf("upload key = %q, want %q", got, want)
	}
	if got, want := SignedDocumentSK("document-1"), "SIGNED_DOCUMENT#document-1"; got != want {
		t.Fatalf("document key = %q, want %q", got, want)
	}
}
