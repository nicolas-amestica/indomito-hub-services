package obtenercotizacionv1

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
)

type fakeDDB struct {
	output *dynamodb.GetItemOutput
}

func (f *fakeDDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return f.output, nil
}
func (f *fakeDDB) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	panic("Query no debe invocarse")
}
func (f *fakeDDB) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	panic("PutItem no debe invocarse")
}
func (f *fakeDDB) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	panic("UpdateItem no debe invocarse")
}
func (f *fakeDDB) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	panic("DeleteItem no debe invocarse")
}

func TestGetQuotationReturnsCompleteFavorite(t *testing.T) {
	const userID = "01JCTXUSER0000000000000000"
	const quotationID = "01JQZ8AAAAAAAAAAAAAAAAAAAA"
	createdAt := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
	item, err := domain.NewFavoriteItem(
		domain.FavoriteKey{UserID: userID, Scope: domain.ScopeQuotation, ID: quotationID},
		"Brasil 2027",
		domain.FavoriteContent{},
		createdAt,
		createdAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatal(err)
	}

	favorite, err := getQuotation(context.Background(), &fakeDDB{output: &dynamodb.GetItemOutput{Item: raw}}, "programs", userID, quotationID)
	if err != nil {
		t.Fatalf("getQuotation devolvió error: %v", err)
	}
	if favorite.ID != quotationID || favorite.Name != "Brasil 2027" {
		t.Fatalf("favorito inesperado: %#v", favorite)
	}
}

func TestGetQuotationReturnsNotFound(t *testing.T) {
	_, err := getQuotation(
		context.Background(),
		&fakeDDB{output: &dynamodb.GetItemOutput{}},
		"programs",
		"01JCTXUSER0000000000000000",
		"01JQZ8AAAAAAAAAAAAAAAAAAAA",
	)
	if err == nil {
		t.Fatal("se esperaba error para una cotización inexistente")
	}
}
