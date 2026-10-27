package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type agentManagementDynamoFake struct {
	putInput *dynamodb.PutItemInput
	putErr   error
}

func (f *agentManagementDynamoFake) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}
func (f *agentManagementDynamoFake) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putInput = input
	return &dynamodb.PutItemOutput{}, f.putErr
}
func (f *agentManagementDynamoFake) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}
func (f *agentManagementDynamoFake) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestDefaultAgentManagedConfigPreservesCurrentCapabilities(t *testing.T) {
	config := DefaultAgentManagedConfig()
	if err := validateManagedConfig(config); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
	if !config.Policies.OfferMeetings || !config.Policies.HumanHandoffEnabled || !config.Policies.HandoffWhatsAppEnabled || !config.Policies.HandoffEmailEnabled {
		t.Fatal("current meeting and handoff capabilities must remain enabled")
	}
	if config.Policies.HandoffPhone != "56963552893" || config.Policies.HandoffEmail != "info@girasindomito.cl" {
		t.Fatal("current recipients must be preserved as initial managed values")
	}
	if len(config.Knowledge) < 4 {
		t.Fatal("current commercial knowledge must be seeded")
	}
}

func TestManagedConfigRequiresAnEnabledHandoffChannel(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.Policies.HandoffWhatsAppEnabled = false
	config.Policies.HandoffEmailEnabled = false
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected handoff channel validation")
	}
}

func TestManagedConfigValidatesHandoffDestinations(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.Policies.HandoffPhone = "invalid"
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected invalid phone")
	}
	config = DefaultAgentManagedConfig()
	config.Policies.HandoffEmail = "invalid"
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected invalid email")
	}
}

func TestManagedConfigRequiresProgramOriginAndActiveDocument(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.Programs = []AgentProgram{{ID: "program", Name: "Bariloche", Destination: "bariloche", Active: true}}
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected origin and document validation")
	}
	config.Programs[0].OriginRegions = []string{"v"}
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected active document validation")
	}
	config.Programs[0].Active = false
	if err := validateManagedConfig(config); err != nil {
		t.Fatalf("inactive program without document should be a valid draft: %v", err)
	}
}

func TestManagedConfigValidatesCommercialPolicies(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.CommercialPolicies = []AgentCommercialPolicy{{ID: "discounts", Title: "Descuentos", Content: "Informa solo descuentos vigentes.", Active: true}}
	if err := validateManagedConfig(config); err != nil {
		t.Fatalf("valid commercial policy rejected: %v", err)
	}
	config.CommercialPolicies[0].Content = ""
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected invalid commercial policy")
	}
}

func TestSaveDraftIfUnchangedUsesOptimisticLock(t *testing.T) {
	db := &agentManagementDynamoFake{}
	service, err := NewAgentManagementService(db, "table")
	if err != nil {
		t.Fatal(err)
	}
	expected := time.Date(2026, time.October, 27, 12, 0, 0, 123, time.UTC)
	if _, err = service.SaveDraftIfUnchanged(context.Background(), DefaultAgentManagedConfig(), expected); err != nil {
		t.Fatal(err)
	}
	if db.putInput.ConditionExpression == nil || *db.putInput.ConditionExpression != "attribute_not_exists(pk) OR updatedAt = :expected" {
		t.Fatalf("missing optimistic condition: %#v", db.putInput.ConditionExpression)
	}
	value, ok := db.putInput.ExpressionAttributeValues[":expected"].(*types.AttributeValueMemberS)
	if !ok || value.Value != expected.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected expected timestamp: %#v", db.putInput.ExpressionAttributeValues[":expected"])
	}

	db.putErr = &types.ConditionalCheckFailedException{}
	if _, err = service.SaveDraftIfUnchanged(context.Background(), DefaultAgentManagedConfig(), expected); !errors.Is(err, ErrAgentConfigChanged) {
		t.Fatalf("expected concurrent change error, got %v", err)
	}
}
