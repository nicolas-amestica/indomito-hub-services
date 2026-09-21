package awsddb

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// stubClient es el doble que usan los tests de los servicios: satisface
// [Client] sin tocar AWS. Vive en el test para verificar que la interfaz sea
// implementable con esfuerzo trivial, que es toda su razon de existir.
type stubClient struct {
	getCalls int
}

func (s *stubClient) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	s.getCalls++
	return &dynamodb.GetItemOutput{}, nil
}

func (s *stubClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (s *stubClient) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (s *stubClient) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (s *stubClient) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// TestClientSurfaceIsExactlyTheFiveAllowedOperations fija la superficie de
// [Client]. Falla tanto si falta una operacion como si aparece una nueva, y
// en particular si alguien agrega Scan: la prohibicion del operador es una
// regla no negociable del proyecto, no una preferencia de estilo, asi que
// conviene que la rompa un test y no una revision de codigo.
func TestClientSurfaceIsExactlyTheFiveAllowedOperations(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil)).Elem()

	got := make([]string, 0, clientType.NumMethod())
	for i := range clientType.NumMethod() {
		got = append(got, clientType.Method(i).Name)
	}
	sort.Strings(got)

	want := []string{"DeleteItem", "GetItem", "PutItem", "Query", "UpdateItem"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("superficie de Client = %v, se esperaba %v", got, want)
	}
}

// TestClientDoesNotExposeScan deja explicito el caso que la regla prohibe,
// para que el motivo quede legible en la salida del test y no solo en la
// comparacion de la lista completa.
func TestClientDoesNotExposeScan(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil)).Elem()

	for _, forbidden := range []string{"Scan", "ScanPages", "BatchExecuteStatement", "ExecuteStatement"} {
		if _, found := clientType.MethodByName(forbidden); found {
			t.Errorf("Client expone %s: el proyecto prohibe recorrer la tabla completa", forbidden)
		}
	}
}

// TestSDKClientSatisfiesClient verifica que el cliente que New devuelve sea
// usable donde se declara [Client]. Es la contraparte en runtime de la
// asercion de compilacion de client.go: construir el cliente no requiere
// credenciales ni red, solo la configuracion.
func TestSDKClientSatisfiesClient(t *testing.T) {
	var client Client = New(aws.Config{Region: "us-east-1"})

	if client == nil {
		t.Fatal("New devolvio un cliente nil")
	}
}

// TestStubSatisfiesClient confirma que un doble de test puede sustituir al
// cliente real, que es la razon por la que la interfaz existe.
func TestStubSatisfiesClient(t *testing.T) {
	stub := &stubClient{}

	var client Client = stub
	if _, err := client.GetItem(context.Background(), &dynamodb.GetItemInput{}); err != nil {
		t.Fatalf("GetItem del doble devolvio error: %v", err)
	}

	if stub.getCalls != 1 {
		t.Errorf("GetItem se invoco %d veces, se esperaba 1", stub.getCalls)
	}
}
