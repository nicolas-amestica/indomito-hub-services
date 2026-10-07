package main

import (
	"context"
	"os"

	"ind-hub-api-gox-sls-pri-gh/services/api-contract/functions"
)

func main() {
	if err := functions.RunLocal(context.Background(), functions.RegisterCreate, functions.RegisterList, functions.RegisterGet, functions.RegisterUpdate, functions.RegisterPDF, functions.RegisterApprovedPDF, functions.RegisterConfiguration, functions.RegisterCreateAmendment, functions.RegisterListAmendments, functions.RegisterApproveAmendment, functions.RegisterApprovedAmendmentPDF, functions.RegisterPrepareSignedDocument, functions.RegisterFinalizeSignedDocument, functions.RegisterListSignedDocuments, functions.RegisterSignedDocumentPDF, functions.RegisterSignedDocumentVersionPDF); err != nil {
		os.Exit(1)
	}
}
