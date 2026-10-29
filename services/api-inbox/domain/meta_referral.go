package domain

import "time"

// MetaReferral identifica el anuncio o publicación que originó una conversación de WhatsApp.
type MetaReferral struct {
	SourceURL    string     `json:"sourceUrl" dynamodbav:"sourceUrl"`
	SourceID     string     `json:"sourceId" dynamodbav:"sourceId"`
	SourceType   string     `json:"sourceType" dynamodbav:"sourceType"`
	Headline     string     `json:"headline" dynamodbav:"headline"`
	Body         string     `json:"body" dynamodbav:"body"`
	MediaType    string     `json:"mediaType" dynamodbav:"mediaType"`
	CtwaClid     string     `json:"-" dynamodbav:"ctwaClid,omitempty"`
	AttributedAt *time.Time `json:"attributedAt" dynamodbav:"attributedAt,omitempty"`
}

func normalizeMetaReferral(referral *MetaReferral) *MetaReferral {
	if referral == nil {
		return nil
	}
	normalized := &MetaReferral{
		SourceURL:  normalizeDerivationText(referral.SourceURL, 2048),
		SourceID:   normalizeDerivationText(referral.SourceID, 200),
		SourceType: normalizeDerivationText(referral.SourceType, 40),
		Headline:   normalizeDerivationText(referral.Headline, 500),
		Body:       normalizeDerivationText(referral.Body, 2000),
		MediaType:  normalizeDerivationText(referral.MediaType, 40),
		CtwaClid:   normalizeDerivationText(referral.CtwaClid, 1000),
	}
	if normalized.SourceID == "" && normalized.CtwaClid == "" {
		return nil
	}
	return normalized
}

func withoutMetaClickID(summary ConversationSummary) ConversationSummary {
	if summary.Referral == nil {
		return summary
	}
	referral := *summary.Referral
	referral.CtwaClid = ""
	summary.Referral = &referral
	return summary
}
