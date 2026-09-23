package functions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"golang.org/x/crypto/bcrypt"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-identity/domain"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

var codeRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)

type envelope struct {
	Data  any       `json:"data,omitempty"`
	Error *apiError `json:"error,omitempty"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func response(status int, data any) (events.APIGatewayV2HTTPResponse, error) {
	b, _ := json.Marshal(envelope{Data: data})
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Headers: map[string]string{"content-type": "application/json"}, Body: string(b)}, nil
}
func fail(status int, code, msg string) (events.APIGatewayV2HTTPResponse, error) {
	b, _ := json.Marshal(envelope{Error: &apiError{code, msg}})
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Headers: map[string]string{"content-type": "application/json"}, Body: string(b)}, nil
}
func Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, e := GetApp(ctx)
	if e != nil {
		return fail(500, "CONFIGURATION_ERROR", "No fue posible iniciar el servicio")
	}
	method := req.RequestContext.HTTP.Method
	path := req.RawPath
	switch {
	case method == "POST" && path == "/auth/login":
		return login(ctx, app, req)
	case method == "GET" && path == "/auth/permisos":
		return permissions(ctx, app, req)
	case method == "GET" && path == "/iam/usuarios":
		return listPartition(ctx, app, "USERS", domain.User{})
	case method == "POST" && path == "/iam/usuarios":
		return saveUser(ctx, app, req)
	case method == "GET" && path == "/iam/perfiles":
		return listPartition(ctx, app, "PROFILES", domain.Profile{})
	case method == "GET" && strings.HasPrefix(path, "/iam/perfiles/") && strings.HasSuffix(path, "/permisos"):
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 4 || !codeRE.MatchString(parts[2]) {
			return fail(400, "VALIDATION_ERROR", "Perfil inválido")
		}
		items, err := profilePermissions(ctx, app, parts[2])
		if err != nil {
			return fail(500, "INTERNAL_ERROR", "No fue posible cargar permisos")
		}
		return response(200, items)
	case method == "POST" && path == "/iam/perfiles":
		return saveProfile(ctx, app, req)
	case method == "GET" && path == "/iam/modulos":
		return listPartition(ctx, app, "MODULES", domain.Module{})
	case method == "POST" && path == "/iam/modulos":
		return saveModule(ctx, app, req)
	case method == "PUT" && strings.HasPrefix(path, "/iam/perfiles/") && strings.HasSuffix(path, "/permisos"):
		return savePermissions(ctx, app, req)
	default:
		return fail(404, "NOT_FOUND", "Ruta no encontrada")
	}
}
func get(ctx context.Context, a *App, pk, sk string, out any) error {
	r, e := a.DDB.GetItem(ctx, &dynamodb.GetItemInput{TableName: &a.Table, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}, ConsistentRead: aws.Bool(true)})
	if e != nil {
		return e
	}
	if len(r.Item) == 0 {
		return errors.New("not found")
	}
	return attributevalue.UnmarshalMap(r.Item, out)
}
func put(ctx context.Context, a *App, v any) error {
	m, e := attributevalue.MarshalMap(v)
	if e != nil {
		return e
	}
	_, e = a.DDB.PutItem(ctx, &dynamodb.PutItemInput{TableName: &a.Table, Item: m})
	return e
}
func query(ctx context.Context, a *App, pk string) ([]map[string]types.AttributeValue, error) {
	r, e := a.DDB.Query(ctx, &dynamodb.QueryInput{TableName: &a.Table, KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}}})
	if e != nil {
		return nil, e
	}
	return r.Items, nil
}
func login(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var in struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if json.Unmarshal([]byte(req.Body), &in) != nil || len(in.Login) > 254 || len(in.Password) < 8 || len(in.Password) > 128 {
		return fail(400, "VALIDATION_ERROR", "Credenciales inválidas")
	}
	var alias domain.LoginAlias
	if get(ctx, a, "LOGIN#"+domain.NormalizeLogin(in.Login), "IDENTITY", &alias) != nil {
		return fail(401, "INVALID_CREDENTIALS", "Usuario o clave incorrectos")
	}
	var u domain.User
	if get(ctx, a, "USER#"+alias.UserID, "IDENTITY", &u) != nil || !u.Active || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		return fail(401, "INVALID_CREDENTIALS", "Usuario o clave incorrectos")
	}
	ps, e := profilePermissions(ctx, a, u.ProfileCode)
	if e != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible cargar los permisos")
	}
	secret, e := a.SSM.GetParameter(ctx, &ssm.GetParameterInput{Name: &a.SecretParam, WithDecryption: aws.Bool(true)})
	if e != nil || secret.Parameter == nil || secret.Parameter.Value == nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible iniciar la sesión")
	}
	token := signToken(u, ps, *secret.Parameter.Value)
	return response(200, map[string]any{"token": token, "expiresIn": 28800, "user": u, "permissions": ps})
}
func signToken(u domain.User, ps []permissionView, secret string) string {
	now := time.Now().Unix()
	eps := []string{}
	allow := map[string]bool{}
	for _, p := range ps {
		eps = append(eps, p.Module.Endpoints...)
		for _, x := range p.Allowances {
			allow[x] = true
		}
	}
	crud := ""
	for _, x := range []string{"c", "r", "u", "d"} {
		if allow[x] {
			crud += x
		}
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, _ := json.Marshal(map[string]any{"body": map[string]any{"allowances": crud, "username": u.Email, "userId": u.ID, "email": u.Email, "rut": u.RUT, "profileCode": u.ProfileCode, "endpoints": eps}, "iat": now, "exp": now + 28800})
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(header + "." + payload))
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type permissionView struct {
	Module     domain.Module `json:"module"`
	Allowances []string      `json:"allowances"`
}

func profilePermissions(ctx context.Context, a *App, code string) ([]permissionView, error) {
	items, e := query(ctx, a, "PROFILE#"+code)
	if e != nil {
		return nil, e
	}
	out := []permissionView{}
	for _, it := range items {
		var p domain.Permission
		if attributevalue.UnmarshalMap(it, &p) != nil {
			continue
		}
		var m domain.Module
		if get(ctx, a, "MODULES", "MODULE#"+p.ModuleCode, &m) == nil && m.Active {
			out = append(out, permissionView{m, p.Allowances})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module.Order < out[j].Module.Order })
	return out, nil
}
func permissions(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	id, e := lambdautil.UserIDFromContext(req)
	if e != nil {
		return fail(401, "UNAUTHORIZED", "Sesión inválida")
	}
	var u domain.User
	if get(ctx, a, "USER#"+id, "IDENTITY", &u) != nil || !u.Active {
		return fail(401, "UNAUTHORIZED", "Sesión inválida")
	}
	p, e := profilePermissions(ctx, a, u.ProfileCode)
	if e != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible cargar permisos")
	}
	return response(200, map[string]any{"user": u, "permissions": p})
}
func listPartition(ctx context.Context, a *App, pk string, kind any) (events.APIGatewayV2HTTPResponse, error) {
	items, e := query(ctx, a, pk)
	if e != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible listar")
	}
	switch kind.(type) {
	case domain.User:
		var out []domain.User
		attributevalue.UnmarshalListOfMaps(items, &out)
		return response(200, out)
	case domain.Profile:
		var out []domain.Profile
		attributevalue.UnmarshalListOfMaps(items, &out)
		return response(200, out)
	default:
		var out []domain.Module
		attributevalue.UnmarshalListOfMaps(items, &out)
		sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
		return response(200, out)
	}
}
func saveUser(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var in struct {
		ID, Name, Email, RUT, Password, ProfileCode string
		Active                                      *bool
	}
	if json.Unmarshal([]byte(req.Body), &in) != nil || in.Name == "" || !strings.Contains(in.Email, "@") || in.RUT == "" || !codeRE.MatchString(in.ProfileCode) {
		return fail(400, "VALIDATION_ERROR", "Datos de usuario inválidos")
	}
	var existing domain.User
	if in.ID != "" {
		_ = get(ctx, a, "USER#"+in.ID, "IDENTITY", &existing)
	} else {
		in.ID = fmt.Sprintf("%d", time.Now().UnixMilli())
	}
	hash := existing.PasswordHash
	if in.Password != "" {
		if len(in.Password) < 10 {
			return fail(400, "VALIDATION_ERROR", "La clave debe tener al menos 10 caracteres")
		}
		b, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if e != nil {
			return fail(500, "INTERNAL_ERROR", "No fue posible proteger la clave")
		}
		hash = string(b)
	}
	if hash == "" {
		return fail(400, "VALIDATION_ERROR", "La clave es obligatoria")
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	u := domain.User{PK: "USER#" + in.ID, SK: "IDENTITY", ID: in.ID, Name: strings.TrimSpace(in.Name), Email: strings.ToLower(strings.TrimSpace(in.Email)), RUT: strings.TrimSpace(in.RUT), PasswordHash: hash, ProfileCode: in.ProfileCode, Active: active, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if e := put(ctx, a, u); e != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible guardar")
	}
	put(ctx, a, domain.LoginAlias{PK: "LOGIN#" + domain.NormalizeLogin(u.Email), SK: "IDENTITY", UserID: u.ID})
	put(ctx, a, domain.LoginAlias{PK: "LOGIN#" + domain.NormalizeLogin(u.RUT), SK: "IDENTITY", UserID: u.ID})
	projection := u
	projection.PK = "USERS"
	projection.SK = "USER#" + u.ID
	projection.PasswordHash = ""
	put(ctx, a, projection)
	return response(200, u)
}
func saveProfile(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var p domain.Profile
	if json.Unmarshal([]byte(req.Body), &p) != nil || !codeRE.MatchString(p.Code) || p.Title == "" {
		return fail(400, "VALIDATION_ERROR", "Perfil inválido")
	}
	p.PK = "PROFILES"
	p.SK = "PROFILE#" + p.Code
	put(ctx, a, p)
	return response(200, p)
}
func saveModule(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var m domain.Module
	if json.Unmarshal([]byte(req.Body), &m) != nil || !codeRE.MatchString(m.Code) || m.Title == "" || !strings.HasPrefix(m.Path, "/") {
		return fail(400, "VALIDATION_ERROR", "Módulo inválido")
	}
	for _, e := range m.Endpoints {
		if !strings.HasPrefix(e, "/") {
			return fail(400, "VALIDATION_ERROR", "Endpoint inválido")
		}
	}
	m.PK = "MODULES"
	m.SK = "MODULE#" + m.Code
	put(ctx, a, m)
	return response(200, m)
}
func savePermissions(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	parts := strings.Split(strings.Trim(req.RawPath, "/"), "/")
	if len(parts) != 4 || !codeRE.MatchString(parts[2]) {
		return fail(400, "VALIDATION_ERROR", "Perfil inválido")
	}
	var items []struct {
		ModuleCode string   `json:"moduleCode"`
		Allowances []string `json:"allowances"`
	}
	if json.Unmarshal([]byte(req.Body), &items) != nil {
		return fail(400, "VALIDATION_ERROR", "Permisos inválidos")
	}
	for _, x := range items {
		if !codeRE.MatchString(x.ModuleCode) {
			return fail(400, "VALIDATION_ERROR", "Módulo inválido")
		}
		for _, v := range x.Allowances {
			if !strings.Contains("crud", v) {
				return fail(400, "VALIDATION_ERROR", "Permiso inválido")
			}
		}
		put(ctx, a, domain.Permission{PK: "PROFILE#" + parts[2], SK: "MODULE#" + x.ModuleCode, ModuleCode: x.ModuleCode, Allowances: x.Allowances})
	}
	return response(http.StatusOK, items)
}
