package dteinbox

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SyncRequest conserva el resultado de una solicitud manual, separado del cursor programado.
type SyncRequest struct {
	ID          string `json:"id" dynamodbav:"id"`
	Actor       string `json:"-" dynamodbav:"actor"`
	Status      string `json:"status" dynamodbav:"status"`
	RequestedAt string `json:"requestedAt" dynamodbav:"requestedAt"`
	StartedAt   string `json:"startedAt" dynamodbav:"startedAt"`
	FinishedAt  string `json:"finishedAt" dynamodbav:"finishedAt"`
	Detail      string `json:"detail" dynamodbav:"detail"`
	Version     int    `json:"-" dynamodbav:"version"`
}

// ErrSyncRequestConflict impide ejecutar o finalizar dos veces una misma solicitud.
var ErrSyncRequestConflict = errors.New("solicitud de sincronización modificada")

func conditionalFailure(err error) bool {
	var conditional *types.ConditionalCheckFailedException
	if errors.As(err, &conditional) {
		return true
	}
	var canceled *types.TransactionCanceledException
	if errors.As(err, &canceled) {
		for _, reason := range canceled.CancellationReasons {
			if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
				return true
			}
		}
	}
	return false
}

// SyncRequest obtiene una solicitud y registra fallas por espera o ejecución vencida.
func (s Store) SyncRequest(ctx context.Context, id string, now time.Time) (*SyncRequest, error) {
	r, err := s.read(ctx, "DTE_INBOX#REQUESTS", id)
	if err != nil || r.Request == nil {
		return nil, err
	}
	job := r.Request
	date, parseErr := time.Parse(time.RFC3339Nano, job.RequestedAt)
	limit := 15 * time.Minute
	if job.Status == "RUNNING" {
		date, parseErr = time.Parse(time.RFC3339Nano, job.StartedAt)
		limit = 6 * time.Minute
	}
	if (job.Status == "PENDING" || job.Status == "RUNNING") && parseErr == nil && now.Sub(date) > limit {
		updated, updateErr := s.TransitionSyncRequest(ctx, *job, "FAILED", "La lectura no terminó dentro del plazo; revisa el estado antes de solicitar otra sincronización.", now)
		if errors.Is(updateErr, ErrSyncRequestConflict) {
			return s.SyncRequest(ctx, id, now)
		}
		return updated, updateErr
	}
	return job, nil
}

// LatestSyncRequest permite retomar el seguimiento al volver a abrir la pantalla.
func (s Store) LatestSyncRequest(ctx context.Context, now time.Time) (*SyncRequest, error) {
	r, err := s.read(ctx, "DTE_INBOX#MANUAL", "META")
	if err != nil || r.RequestID == "" {
		return nil, err
	}
	return s.SyncRequest(ctx, r.RequestID, now)
}

// BeginSyncRequest crea solicitud y límite de frecuencia en una única transacción.
func (s Store) BeginSyncRequest(ctx context.Context, actor, id string, now time.Time) (*SyncRequest, bool, error) {
	previous, err := s.LatestSyncRequest(ctx, now)
	if err != nil {
		return nil, false, err
	}
	if previous != nil && (previous.Status == "PENDING" || previous.Status == "RUNNING") {
		return previous, false, nil
	}
	job := &SyncRequest{ID: id, Actor: actor, Status: "PENDING", RequestedAt: now.UTC().Format(time.RFC3339Nano), Version: 1}
	item, err := attributevalue.MarshalMap(row{PK: "DTE_INBOX#REQUESTS", SK: id, Request: job})
	if err != nil {
		return nil, false, err
	}
	control, err := attributevalue.MarshalMap(row{PK: "DTE_INBOX#MANUAL", SK: "META", RequestID: id})
	if err != nil {
		return nil, false, err
	}
	// La condición compara epoch, independiente de zona horaria.
	control["expiresAt"], err = attributevalue.Marshal(now.Add(time.Minute).Unix())
	if err != nil {
		return nil, false, err
	}
	current, err := attributevalue.Marshal(now.Unix())
	if err != nil {
		return nil, false, err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{ClientRequestToken: &id, TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.Table, Item: control, ConditionExpression: aws.String("attribute_not_exists(pk) OR expiresAt < :now"), ExpressionAttributeValues: map[string]types.AttributeValue{":now": current}}},
		{Put: &types.Put{TableName: &s.Table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	if conditionalFailure(err) {
		latest, e := s.LatestSyncRequest(ctx, now)
		return latest, false, e
	}
	if err != nil {
		return nil, false, err
	}
	return job, true, nil
}

// TransitionSyncRequest aplica compare-and-swap para proteger resultados terminales.
func (s Store) TransitionSyncRequest(ctx context.Context, current SyncRequest, status, detail string, now time.Time) (*SyncRequest, error) {
	valid := (current.Status == "PENDING" && (status == "RUNNING" || status == "FAILED")) || (current.Status == "RUNNING" && (status == "PENDING" || status == "COMPLETED" || status == "FAILED"))
	if !valid {
		return nil, ErrSyncRequestConflict
	}
	next := current
	next.Version++
	next.Status = status
	next.Detail = detail
	if status == "RUNNING" {
		next.StartedAt = now.UTC().Format(time.RFC3339Nano)
	}
	if status == "COMPLETED" || status == "FAILED" {
		next.FinishedAt = now.UTC().Format(time.RFC3339Nano)
	}
	item, err := attributevalue.MarshalMap(row{PK: "DTE_INBOX#REQUESTS", SK: current.ID, Request: &next})
	if err != nil {
		return nil, err
	}
	version, err := attributevalue.Marshal(current.Version)
	if err != nil {
		return nil, err
	}
	_, err = s.DB.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.Table, Item: item, ConditionExpression: aws.String("#job.#version = :version"), ExpressionAttributeNames: map[string]string{"#job": "request", "#version": "version"}, ExpressionAttributeValues: map[string]types.AttributeValue{":version": version}})
	if conditionalFailure(err) {
		return nil, ErrSyncRequestConflict
	}
	if err != nil {
		return nil, err
	}
	return &next, nil
}
