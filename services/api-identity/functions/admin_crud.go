package functions

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-identity/domain"
)

const (
	appPK                = "APP#INDOMITO_HUB"
	profilePermissionsPK = "APP#PROFILE#PERMISSIONS"
	userProfilesPK       = "APP#PROFILE#USER"
)

func pathID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func deleteItem(ctx context.Context, a *App, pk, sk string) error {
	_, err := a.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &a.Table,
		Key:       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}},
	})
	return err
}

func listUsers(ctx context.Context, a *App) (events.APIGatewayV2HTTPResponse, error) {
	items, err := query(ctx, a, "USERS")
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible listar usuarios")
	}
	var out []domain.User
	if err = attributevalue.UnmarshalListOfMaps(items, &out); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible leer usuarios")
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return response(200, out)
}

func updateUser(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	id := pathID(req.RawPath)
	if id == "" {
		return fail(400, "VALIDATION_ERROR", "Usuario inválido")
	}
	var existing domain.User
	if get(ctx, a, "USER#"+id, "IDENTITY", &existing) != nil {
		return fail(404, "NOT_FOUND", "Usuario no encontrado")
	}
	var in struct {
		Name, Email, RUT, Password, ProfileCode string
		Active                                  *bool
	}
	if json.Unmarshal([]byte(req.Body), &in) != nil {
		return fail(400, "VALIDATION_ERROR", "Datos de usuario inválidos")
	}
	if in.Name != "" {
		existing.Name = strings.TrimSpace(in.Name)
	}
	if in.Email != "" {
		existing.Email = strings.ToLower(strings.TrimSpace(in.Email))
	}
	if in.RUT != "" {
		existing.RUT = strings.TrimSpace(in.RUT)
	}
	if in.ProfileCode != "" {
		existing.ProfileCode = in.ProfileCode
	}
	if in.Active != nil {
		existing.Active = *in.Active
	}
	// Reutiliza la validación y el hashing centralizados del alta.
	payload, _ := json.Marshal(map[string]any{"id": existing.ID, "name": existing.Name, "email": existing.Email, "rut": existing.RUT, "password": in.Password, "profileCode": existing.ProfileCode, "active": existing.Active})
	req.Body = string(payload)
	return saveUser(ctx, a, req)
}

func deleteUser(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	id := pathID(req.RawPath)
	var u domain.User
	if id == "" || get(ctx, a, "USER#"+id, "IDENTITY", &u) != nil {
		return fail(404, "NOT_FOUND", "Usuario no encontrado")
	}
	if err := deleteItem(ctx, a, "USER#"+id, "IDENTITY"); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible eliminar el usuario")
	}
	_ = deleteItem(ctx, a, "USERS", "USER#"+id)
	_ = deleteItem(ctx, a, "LOGIN#"+domain.NormalizeLogin(u.Email), "IDENTITY")
	_ = deleteItem(ctx, a, "LOGIN#"+domain.NormalizeLogin(u.RUT), "IDENTITY")
	return response(http.StatusOK, map[string]bool{"deleted": true})
}

func listProfiles(ctx context.Context, a *App) (events.APIGatewayV2HTTPResponse, error) {
	items, err := query(ctx, a, profilePermissionsPK)
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible listar perfiles")
	}
	out := []domain.Profile{}
	for _, item := range items {
		var p domain.Profile
		if attributevalue.UnmarshalMap(item, &p) != nil || !strings.HasSuffix(p.SK, "#METADATA") {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 { // Compatibilidad durante la migración desde el primer modelo.
		legacy, _ := query(ctx, a, "PROFILES")
		_ = attributevalue.UnmarshalListOfMaps(legacy, &out)
	}
	users, _ := query(ctx, a, "USERS")
	var allUsers []domain.User
	_ = attributevalue.UnmarshalListOfMaps(users, &allUsers)
	for i := range out {
		permissions, _ := queryPrefix(ctx, a, profilePermissionsPK, "PFL#"+out[i].Code+"#"+appPK+"#LV1#")
		out[i].PermissionCount = len(permissions)
		for _, user := range allUsers {
			if user.ProfileCode == out[i].Code {
				out[i].UserCount++
			}
		}
		out[i].Deprecated = !out[i].Active
		if out[i].Tooltip == "" {
			out[i].Tooltip = out[i].Description
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return response(200, out)
}

func decodeProfile(req events.APIGatewayV2HTTPRequest, code string) (domain.Profile, bool) {
	var p domain.Profile
	if json.Unmarshal([]byte(req.Body), &p) != nil {
		return p, false
	}
	if code != "" {
		p.Code = code
	}
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	p.Title = strings.TrimSpace(p.Title)
	if p.Description == "" {
		p.Description = p.Tooltip
	}
	if p.Tooltip == "" {
		p.Tooltip = p.Description
	}
	if p.Deprecated {
		p.Active = false
	}
	return p, codeRE.MatchString(p.Code) && p.Title != ""
}

func createProfile(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	p, ok := decodeProfile(req, "")
	if !ok {
		return fail(400, "VALIDATION_ERROR", "Perfil inválido")
	}
	if get(ctx, a, profilePermissionsPK, "PFL#"+p.Code+"#METADATA", &domain.Profile{}) == nil {
		return fail(409, "PROFILE_ALREADY_EXISTS", "El perfil ya existe")
	}
	p.PK, p.SK = profilePermissionsPK, "PFL#"+p.Code+"#METADATA"
	if err := put(ctx, a, p); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible crear el perfil")
	}
	return response(201, p)
}

func updateProfile(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	code := pathID(req.RawPath)
	var old domain.Profile
	if get(ctx, a, profilePermissionsPK, "PFL#"+code+"#METADATA", &old) != nil {
		return fail(404, "NOT_FOUND", "Perfil no encontrado")
	}
	p, ok := decodeProfile(req, code)
	if !ok {
		return fail(400, "VALIDATION_ERROR", "Perfil inválido")
	}
	p.PK, p.SK = old.PK, old.SK
	if err := put(ctx, a, p); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible actualizar el perfil")
	}
	return response(200, p)
}

func deleteProfile(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	code := pathID(req.RawPath)
	if !codeRE.MatchString(code) {
		return fail(400, "VALIDATION_ERROR", "Perfil inválido")
	}
	users, _ := query(ctx, a, "USERS")
	var all []domain.User
	_ = attributevalue.UnmarshalListOfMaps(users, &all)
	for _, u := range all {
		if u.ProfileCode == code {
			return fail(409, "PROFILE_HAS_USERS", "No puedes eliminar un perfil que tiene usuarios asignados")
		}
	}
	permissions, _ := queryPrefix(ctx, a, profilePermissionsPK, "PFL#"+code+"#")
	for _, item := range permissions {
		if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
			_ = deleteItem(ctx, a, profilePermissionsPK, sk.Value)
		}
	}
	return response(200, map[string]bool{"deleted": true})
}

func listModules(ctx context.Context, a *App) (events.APIGatewayV2HTTPResponse, error) {
	items, err := query(ctx, a, appPK)
	if err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible listar módulos")
	}
	var out []domain.Module
	if attributevalue.UnmarshalListOfMaps(items, &out) != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible leer módulos")
	}
	if len(out) == 0 {
		legacy, _ := query(ctx, a, "MODULES")
		_ = attributevalue.UnmarshalListOfMaps(legacy, &out)
	}
	for i := range out {
		if out[i].Level == "" {
			out[i].Level = "LV1"
		}
		out[i].Deprecated = !out[i].Active
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentCode == out[j].ParentCode {
			return out[i].Order < out[j].Order
		}
		return out[i].ParentCode < out[j].ParentCode
	})
	return response(200, out)
}

func decodeModule(req events.APIGatewayV2HTTPRequest, code string) (domain.Module, bool) {
	var m domain.Module
	if json.Unmarshal([]byte(req.Body), &m) != nil {
		return m, false
	}
	if code != "" {
		m.Code = code
	}
	m.Code = strings.ToUpper(strings.TrimSpace(m.Code))
	m.ParentCode = strings.ToUpper(strings.TrimSpace(m.ParentCode))
	if m.Level == "" {
		if m.ParentCode == "" {
			m.Level = "LV1"
		} else {
			m.Level = "LV2"
		}
	}
	if m.Deprecated {
		m.Active = false
	}
	if m.CreatedAt == "" {
		m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	validParent := m.Level == "LV1" || (m.Level == "LV2" && codeRE.MatchString(m.ParentCode))
	return m, codeRE.MatchString(m.Code) && m.Title != "" && strings.HasPrefix(m.Path, "/") && validParent
}

func createModule(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	m, ok := decodeModule(req, "")
	if !ok {
		return fail(400, "VALIDATION_ERROR", "Módulo inválido")
	}
	moduleSK := appPK + "#LV1#" + m.Code
	if m.Level == "LV2" {
		moduleSK = appPK + "#LV1#" + m.ParentCode + "#LV2#" + m.Code
	}
	if get(ctx, a, appPK, moduleSK, &domain.Module{}) == nil {
		return fail(409, "MODULE_ALREADY_EXISTS", "El módulo ya existe")
	}
	if m.Level == "LV2" {
		var parent domain.Module
		if get(ctx, a, appPK, appPK+"#LV1#"+m.ParentCode, &parent) != nil {
			return fail(400, "PARENT_NOT_FOUND", "El módulo padre no existe")
		}
	}
	m.PK, m.SK = appPK, moduleSK
	if err := put(ctx, a, m); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible crear el módulo")
	}
	return response(201, m)
}

func updateModule(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	code := pathID(req.RawPath)
	var old domain.Module
	modules, _ := listModuleRecords(ctx, a)
	for _, candidate := range modules {
		if candidate.Code == code {
			old = candidate
			break
		}
	}
	if old.Code == "" {
		return fail(404, "NOT_FOUND", "Módulo no encontrado")
	}
	m, ok := decodeModule(req, code)
	if !ok {
		return fail(400, "VALIDATION_ERROR", "Módulo inválido")
	}
	m.PK, m.SK, m.CreatedAt = old.PK, old.SK, old.CreatedAt
	if err := put(ctx, a, m); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible actualizar el módulo")
	}
	return response(200, m)
}

func deleteModule(ctx context.Context, a *App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	code := pathID(req.RawPath)
	var module domain.Module
	modules, _ := listModuleRecords(ctx, a)
	for _, candidate := range modules {
		if candidate.Code == code {
			module = candidate
			break
		}
	}
	if module.Code == "" {
		return fail(404, "NOT_FOUND", "Módulo no encontrado")
	}
	for _, child := range modules {
		if child.ParentCode == code {
			_ = deleteItem(ctx, a, child.PK, child.SK)
		}
	}
	if err := deleteItem(ctx, a, module.PK, module.SK); err != nil {
		return fail(500, "INTERNAL_ERROR", "No fue posible eliminar el módulo")
	}
	return response(200, map[string]bool{"deleted": true})
}

var _ = aws.Bool

func queryPrefix(ctx context.Context, a *App, pk, prefix string) ([]map[string]types.AttributeValue, error) {
	r, err := a.DDB.Query(ctx, &dynamodb.QueryInput{TableName: &a.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :sk)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":sk": &types.AttributeValueMemberS{Value: prefix}}})
	if err != nil {
		return nil, err
	}
	return r.Items, nil
}

func listModuleRecords(ctx context.Context, a *App) ([]domain.Module, error) {
	items, err := query(ctx, a, appPK)
	if err != nil {
		return nil, err
	}
	var out []domain.Module
	err = attributevalue.UnmarshalListOfMaps(items, &out)
	return out, err
}
