package rates

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/domain"
)

// WriteRateSnapshot sobrescribe el último snapshot válido de tasas.
//
// La operación recibe el contexto de la invocación para que el timeout de la
// Lambda cancele DynamoDB. El llamador decide si el error es bloqueante: en el
// endpoint no lo es, porque las tasas frescas ya están disponibles y la
// escritura solo prepara el respaldo de una solicitud futura.
func WriteRateSnapshot(
	ctx context.Context,
	ddb awsddb.Client,
	tableName string,
	snapshot program.ExchangeSnapshot,
	fetchedAt time.Time,
) error {
	item, err := attributevalue.MarshalMap(domain.NewRateItem(snapshot, fetchedAt))
	if err != nil {
		return fmt.Errorf("no se pudo serializar el snapshot de tasas: %w", err)
	}

	if _, err := ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	}); err != nil {
		return fmt.Errorf("no se pudo escribir el snapshot de tasas: %w", err)
	}

	return nil
}

// ReadRateSnapshot lee el último snapshot por su clave completa.
//
// found es false cuando la tabla aún no contiene un snapshot. Un error de
// DynamoDB o un ítem ilegible se devuelve como INTERNAL_ERROR: no se confunde
// una falla de persistencia con la ausencia legítima que produce el 502 del
// Requirement 14.9.
func ReadRateSnapshot(
	ctx context.Context,
	ddb awsddb.Client,
	tableName string,
) (snapshot domain.RateItem, found bool, err error) {
	key, err := attributevalue.MarshalMap(domain.RatesSnapshotKey())
	if err != nil {
		return domain.RateItem{}, false, apperr.Internal(
			fmt.Errorf("no se pudo serializar la clave del snapshot de tasas: %w", err),
		)
	}

	output, err := ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(tableName),
		Key:            key,
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return domain.RateItem{}, false, apperr.Internal(
			fmt.Errorf("no se pudo leer el snapshot de tasas: %w", err),
		)
	}

	if len(output.Item) == 0 {
		return domain.RateItem{}, false, nil
	}

	var item domain.RateItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.RateItem{}, false, apperr.Internal(
			fmt.Errorf("no se pudo interpretar el snapshot de tasas: %w", err),
		)
	}

	if item.PK != domain.RatesPK || item.SK != domain.RatesLatestSK ||
		item.Date == "" || item.UsdToClp <= 0 || item.BrlToClp <= 0 || item.FetchedAt.IsZero() {
		return domain.RateItem{}, false, apperr.Internal(
			fmt.Errorf("el snapshot de tasas persistido está incompleto"),
		)
	}

	return item, true, nil
}
