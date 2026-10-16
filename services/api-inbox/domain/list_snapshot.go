package domain

import (
	"fmt"
	"time"
)

const (
	listSnapshotChunkSize = 100
	listSnapshotTTL       = 15 * time.Minute
	listSnapshotMaxTTL    = 30 * time.Minute
)

type listSnapshotMeta struct {
	PK                string    `dynamodbav:"pk"`
	SK                string    `dynamodbav:"sk"`
	OwnerID           string    `dynamodbav:"ownerId"`
	QueryHash         string    `dynamodbav:"queryHash"`
	Cutoff            uint64    `dynamodbav:"cutoff"`
	TotalItems        int       `dynamodbav:"totalItems"`
	UnreadTotal       int       `dynamodbav:"unreadTotal"`
	ChunkCount        int       `dynamodbav:"chunkCount"`
	Slot              int       `dynamodbav:"slot"`
	CreatedAt         time.Time `dynamodbav:"createdAt"`
	LastAccessAt      time.Time `dynamodbav:"lastAccessAt"`
	AbsoluteExpiresAt time.Time `dynamodbav:"absoluteExpiresAt"`
	ExpiresAt         int64     `dynamodbav:"expiresAt"`
}

type listSnapshotChunk struct {
	PK        string                `dynamodbav:"pk"`
	SK        string                `dynamodbav:"sk"`
	Items     []ConversationSummary `dynamodbav:"items"`
	ExpiresAt int64                 `dynamodbav:"expiresAt"`
}

type listSnapshotDocuments struct {
	meta   listSnapshotMeta
	chunks []listSnapshotChunk
}

func buildListSnapshotDocuments(input ListSnapshotCreate, sessionID string, slot int, now time.Time) listSnapshotDocuments {
	partitionKey := "LIST_RESULT#" + sessionID
	absoluteExpiresAt := now.Add(listSnapshotMaxTTL)
	initialExpiresAt := now.Add(listSnapshotTTL).Unix()
	unreadTotal := 0
	for _, item := range input.Items {
		unreadTotal += item.UnreadCount
	}
	chunkCount := (len(input.Items) + listSnapshotChunkSize - 1) / listSnapshotChunkSize
	documents := listSnapshotDocuments{
		meta: listSnapshotMeta{
			PK: partitionKey, SK: "META", OwnerID: input.OwnerID, QueryHash: input.QueryHash,
			Cutoff: input.Cutoff, TotalItems: len(input.Items), UnreadTotal: unreadTotal,
			ChunkCount: chunkCount, Slot: slot, CreatedAt: now, LastAccessAt: now,
			AbsoluteExpiresAt: absoluteExpiresAt, ExpiresAt: initialExpiresAt,
		},
		chunks: make([]listSnapshotChunk, 0, chunkCount),
	}
	for start, index := 0, 0; start < len(input.Items); start, index = start+listSnapshotChunkSize, index+1 {
		end := min(start+listSnapshotChunkSize, len(input.Items))
		items := append([]ConversationSummary(nil), input.Items[start:end]...)
		documents.chunks = append(documents.chunks, listSnapshotChunk{
			PK: partitionKey, SK: fmt.Sprintf("CHUNK#%06d", index), Items: items, ExpiresAt: initialExpiresAt,
		})
	}
	return documents
}

func renewedSnapshotExpiry(now, absoluteExpiresAt time.Time) int64 {
	renewed := now.Add(listSnapshotTTL)
	if renewed.After(absoluteExpiresAt) {
		return absoluteExpiresAt.Unix()
	}
	return renewed.Unix()
}
