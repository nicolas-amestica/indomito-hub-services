package domain

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const ConversationListPageSize = 200

var ErrSnapshotLimitReached = errors.New("inbox snapshot limit reached")

type ListSnapshotCreate struct {
	OwnerID   string
	QueryHash string
	Cutoff    uint64
	Items     []ConversationSummary
}

type ListSnapshotPageRequest struct {
	OwnerID   string
	QueryHash string
	SessionID string
	Slot      int
	Page      int
	PageSize  int
}

type ListSnapshotPage struct {
	SessionID         string
	Slot              int
	Page              int
	Items             []ConversationSummary
	TotalItems        int
	UnreadTotal       int
	Cutoff            uint64
	CreatedAt         time.Time
	AbsoluteExpiresAt time.Time
	HasMore           bool
}

// ListSnapshotStore persiste y pagina resúmenes inmutables de una sesión.
// La implementación debe validar owner, query hash, slot y vigencia al leer.
type ListSnapshotStore interface {
	Create(context.Context, ListSnapshotCreate) (ListSnapshotPage, error)
	ReadPage(context.Context, ListSnapshotPageRequest) (ListSnapshotPage, error)
}

// ConversationListService orquesta materialización, snapshot y cursor firmado.
type ConversationListService struct {
	reader          ListReader
	snapshots       ListSnapshotStore
	cursors         *ListCursorCodec
	tableName       string
	maxMaterialized int
	now             func() time.Time
}

func NewConversationListService(reader ListReader, snapshots ListSnapshotStore, cursors *ListCursorCodec, tableName string, maxMaterialized int, now func() time.Time) (*ConversationListService, error) {
	if reader == nil || snapshots == nil || cursors == nil || strings.TrimSpace(tableName) == "" || maxMaterialized <= 0 {
		return nil, errors.New("configuracion invalida del servicio de lista")
	}
	if now == nil {
		now = time.Now
	}
	return &ConversationListService{reader: reader, snapshots: snapshots, cursors: cursors, tableName: tableName, maxMaterialized: maxMaterialized, now: now}, nil
}

// FirstPage materializa y congela una nueva sesión de lista.
func (service *ConversationListService) FirstPage(ctx context.Context, ownerID, query string) (ConversationListResponse, error) {
	queryHash := ListQueryHash(query)
	cutoff, err := ReadListCutoff(ctx, service.reader, service.tableName)
	if err != nil {
		return ConversationListResponse{}, err
	}
	items, err := MaterializeConversationList(ctx, service.reader, service.tableName, cutoff, query, service.maxMaterialized)
	if err != nil {
		return ConversationListResponse{}, err
	}
	page, err := service.snapshots.Create(ctx, ListSnapshotCreate{OwnerID: ownerID, QueryHash: queryHash, Cutoff: cutoff, Items: items})
	if err != nil {
		return ConversationListResponse{}, fmt.Errorf("create list snapshot: %w", err)
	}
	return service.response(ownerID, queryHash, page)
}

// NextPage valida el cursor y lee exclusivamente el snapshot congelado.
func (service *ConversationListService) NextPage(ctx context.Context, ownerID, query, token string) (ConversationListResponse, error) {
	queryHash := ListQueryHash(query)
	cursor, err := service.cursors.Decode(token, ownerID, queryHash)
	if err != nil {
		return ConversationListResponse{}, err
	}
	page, err := service.snapshots.ReadPage(ctx, ListSnapshotPageRequest{
		OwnerID: ownerID, QueryHash: queryHash, SessionID: cursor.SessionID,
		Slot: cursor.Slot, Page: cursor.Page, PageSize: ConversationListPageSize,
	})
	if err != nil {
		return ConversationListResponse{}, fmt.Errorf("read list snapshot: %w", err)
	}
	if page.SessionID != cursor.SessionID || page.Slot != cursor.Slot || page.Page != cursor.Page || !page.AbsoluteExpiresAt.Equal(time.Unix(cursor.AbsoluteExpiresAt, 0)) {
		return ConversationListResponse{}, ErrInvalidCursor
	}
	return service.response(ownerID, queryHash, page)
}

func (service *ConversationListService) response(ownerID, queryHash string, page ListSnapshotPage) (ConversationListResponse, error) {
	var nextCursor *string
	if page.HasMore {
		token, err := service.cursors.Encode(ListCursor{
			Version: 1, SessionID: page.SessionID, Page: page.Page + 1,
			QueryHash: queryHash, OwnerHash: HashCursorBinding(ownerID), Slot: page.Slot,
			IssuedAt: service.now().Unix(), AbsoluteExpiresAt: page.AbsoluteExpiresAt.Unix(),
		})
		if err != nil {
			return ConversationListResponse{}, fmt.Errorf("encode next list cursor: %w", err)
		}
		nextCursor = &token
	}
	totalPages := 0
	if page.TotalItems > 0 {
		totalPages = (page.TotalItems + ConversationListPageSize - 1) / ConversationListPageSize
	}
	return ConversationListResponse{
		Data:       page.Items,
		PageInfo:   PageInfo{Page: page.Page, PageSize: ConversationListPageSize, TotalItems: page.TotalItems, TotalPages: totalPages},
		NextCursor: nextCursor, UnreadTotal: page.UnreadTotal,
		Watermark: ListWatermark{Stream: "list", Sequence: page.Cutoff},
	}, nil
}

// ListQueryHash fija búsqueda normalizada y orden contractual sin exponerlos en el cursor.
func ListQueryHash(query string) string {
	hash := sha256.Sum256([]byte("v1|activity_desc|" + normalizeSearch(query)))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}
