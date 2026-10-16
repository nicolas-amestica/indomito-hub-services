package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type DetailWatermark struct {
	Stream   string `json:"stream"`
	Sequence uint64 `json:"sequence"`
}

func (service *ConversationDetailService) Delta(ctx context.Context, ownerID, phone string, since time.Time) (ConversationDetailResponse, error) {
	snapshot, err := ReadConversationDetailDelta(ctx, service.reader, service.tableName, ownerID, phone, since)
	if err != nil {
		return ConversationDetailResponse{}, err
	}
	requestedSince := since.UTC().Format(time.RFC3339Nano)
	if snapshot.Messages == nil {
		snapshot.Messages = []Message{}
	}
	if snapshot.NonTerminalSends == nil {
		snapshot.NonTerminalSends = []NonTerminalSend{}
	}
	return ConversationDetailResponse{Mode: "delta", Conversation: &snapshot.Conversation, Messages: snapshot.Messages, NonTerminalSends: snapshot.NonTerminalSends, Watermark: DetailWatermark{Stream: "detail:" + phone, Sequence: snapshot.Sequence}, RequestedSince: &requestedSince}, nil
}

type ConversationDetailResponse struct {
	Mode             string               `json:"mode"`
	Conversation     *ConversationSummary `json:"conversation"`
	Messages         []Message            `json:"messages"`
	HasMore          bool                 `json:"hasMore"`
	NextCursor       *string              `json:"nextCursor"`
	NonTerminalSends []NonTerminalSend    `json:"nonTerminalSends"`
	Watermark        DetailWatermark      `json:"watermark"`
	RequestedBefore  *string              `json:"requestedBefore,omitempty"`
	RequestedSince   *string              `json:"requestedSince,omitempty"`
}

// ConversationDetailService construye páginas full y before con cursor opaco.
type ConversationDetailService struct {
	reader    detailReader
	cursors   *ListCursorCodec
	tableName string
}

func NewConversationDetailService(reader detailReader, cursors *ListCursorCodec, tableName string) (*ConversationDetailService, error) {
	if reader == nil || cursors == nil || tableName == "" {
		return nil, errors.New("invalid conversation detail service configuration")
	}
	return &ConversationDetailService{reader: reader, cursors: cursors, tableName: tableName}, nil
}

func (service *ConversationDetailService) Page(ctx context.Context, ownerID, phone, beforeToken string, pageSize int) (ConversationDetailResponse, error) {
	beforeSK := ""
	mode := "full"
	if beforeToken != "" {
		cursor, err := service.cursors.decodeDetail(beforeToken, ownerID, phone)
		if err != nil {
			return ConversationDetailResponse{}, err
		}
		beforeSK, mode = cursor.BeforeSK, "before"
	}
	snapshot, err := ReadConversationDetail(ctx, service.reader, service.tableName, ownerID, phone, beforeSK, pageSize)
	if err != nil {
		return ConversationDetailResponse{}, err
	}
	var nextCursor *string
	if snapshot.HasMore {
		token, err := service.cursors.encodeDetail(ownerID, phone, snapshot.OldestMessageSK)
		if err != nil {
			return ConversationDetailResponse{}, fmt.Errorf("encode detail cursor: %w", err)
		}
		nextCursor = &token
	}
	conversation := &snapshot.Conversation
	sends := snapshot.NonTerminalSends
	var requestedBefore *string
	if mode == "before" {
		conversation = nil
		sends = []NonTerminalSend{}
		requestedBefore = &beforeToken
	}
	if snapshot.Messages == nil {
		snapshot.Messages = []Message{}
	}
	if sends == nil {
		sends = []NonTerminalSend{}
	}
	return ConversationDetailResponse{Mode: mode, Conversation: conversation, Messages: snapshot.Messages, HasMore: snapshot.HasMore, NextCursor: nextCursor, NonTerminalSends: sends, Watermark: DetailWatermark{Stream: "detail:" + phone, Sequence: snapshot.Sequence}, RequestedBefore: requestedBefore}, nil
}
