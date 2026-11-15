package domain

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// MeetingHost contiene la asignación del anfitrión del catálogo compartido.
type MeetingHost struct {
	SK     string `dynamodbav:"sk"`
	Name   string `dynamodbav:"name"`
	Color  string `dynamodbav:"color"`
	Active bool   `dynamodbav:"active"`
}

type meetingHostReader interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// SetDefaultMeetingHost consulta la selección vigente únicamente al crear una reunión.
func (t *AgentToolbox) SetDefaultMeetingHost(db meetingHostReader, table string) {
	t.defaultMeetingHost = func(ctx context.Context) (*MeetingHost, error) {
		if table == "" {
			return nil, errors.New("configurations table is required for meeting host selection")
		}
		read := func(sk string) (*dynamodb.GetItemOutput, error) {
			return db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: "CONFIGURATION"}, "sk": &types.AttributeValueMemberS{Value: sk},
			}})
		}
		pointer, err := read("MEET_HOST_DEFAULT")
		if err != nil {
			return nil, err
		}
		var selection struct {
			HostID string `dynamodbav:"hostId"`
		}
		if err = attributevalue.UnmarshalMap(pointer.Item, &selection); err != nil {
			return nil, err
		}
		if selection.HostID == "" {
			return nil, nil
		}
		result, err := read("MEET_HOST#" + selection.HostID)
		if err != nil {
			return nil, err
		}
		var host MeetingHost
		if err = attributevalue.UnmarshalMap(result.Item, &host); err != nil {
			return nil, err
		}
		if !host.Active || host.SK == "" {
			return nil, nil
		}
		return &host, nil
	}
}
