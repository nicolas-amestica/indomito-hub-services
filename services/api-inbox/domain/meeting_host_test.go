package domain

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"testing"
)

type hostLookupMock struct {
	items map[string]map[string]types.AttributeValue
	err   error
	keys  []string
}

func (m *hostLookupMock) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if input.ConsistentRead == nil || !*input.ConsistentRead {
		return nil, errors.New("consistent read required")
	}
	if input.Key["pk"].(*types.AttributeValueMemberS).Value != "CONFIGURATION" {
		return nil, errors.New("invalid partition")
	}
	sk := input.Key["sk"].(*types.AttributeValueMemberS).Value
	m.keys = append(m.keys, sk)
	return &dynamodb.GetItemOutput{Item: m.items[sk]}, m.err
}
func TestDefaultMeetingHostUsesCurrentCatalogPointer(t *testing.T) {
	db := &hostLookupMock{items: map[string]map[string]types.AttributeValue{
		"MEET_HOST_DEFAULT": {"hostId": &types.AttributeValueMemberS{Value: "one"}},
		"MEET_HOST#one":     {"sk": &types.AttributeValueMemberS{Value: "MEET_HOST#one"}, "name": &types.AttributeValueMemberS{Value: "Primero"}, "color": &types.AttributeValueMemberS{Value: "#6366f1"}, "active": &types.AttributeValueMemberBOOL{Value: true}},
		"MEET_HOST#two":     {"sk": &types.AttributeValueMemberS{Value: "MEET_HOST#two"}, "name": &types.AttributeValueMemberS{Value: "Segundo"}, "active": &types.AttributeValueMemberBOOL{Value: true}},
	}}
	toolbox := &AgentToolbox{}
	toolbox.SetDefaultMeetingHost(db, "configurations")
	host, err := toolbox.defaultMeetingHost(context.Background())
	if err != nil || host.SK != "MEET_HOST#one" || host.Name != "Primero" || host.Color != "#6366f1" {
		t.Fatalf("unexpected host: %+v %v", host, err)
	}
	db.items["MEET_HOST_DEFAULT"]["hostId"] = &types.AttributeValueMemberS{Value: "two"}
	host, err = toolbox.defaultMeetingHost(context.Background())
	if err != nil || host.SK != "MEET_HOST#two" {
		t.Fatalf("stale default host: %+v %v", host, err)
	}
	db.items["MEET_HOST#two"]["active"] = &types.AttributeValueMemberBOOL{Value: false}
	host, err = toolbox.defaultMeetingHost(context.Background())
	if err != nil || host != nil {
		t.Fatalf("inactive default host: %+v %v", host, err)
	}
}
func TestDefaultMeetingHostDoesNotHideReadFailures(t *testing.T) {
	db := &hostLookupMock{err: errors.New("denied")}
	toolbox := &AgentToolbox{}
	toolbox.SetDefaultMeetingHost(db, "configurations")
	if _, err := toolbox.defaultMeetingHost(context.Background()); err == nil {
		t.Fatal("expected read failure")
	}
	db.err = nil
	if host, err := toolbox.defaultMeetingHost(context.Background()); host != nil || err != nil {
		t.Fatalf("missing pointer: %+v %v", host, err)
	}
}
