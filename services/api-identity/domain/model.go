package domain

import "strings"

type User struct {
	PK           string `dynamodbav:"pk" json:"-"`
	SK           string `dynamodbav:"sk" json:"-"`
	ID           string `dynamodbav:"id" json:"id"`
	Name         string `dynamodbav:"name" json:"name"`
	Email        string `dynamodbav:"email" json:"email"`
	RUT          string `dynamodbav:"rut" json:"rut"`
	PasswordHash string `dynamodbav:"passwordHash" json:"-"`
	ProfileCode  string `dynamodbav:"profileCode" json:"profileCode"`
	Active       bool   `dynamodbav:"active" json:"active"`
	CreatedAt    string `dynamodbav:"createdAt" json:"createdAt"`
}
type Module struct {
	PK        string   `dynamodbav:"pk" json:"-"`
	SK        string   `dynamodbav:"sk" json:"-"`
	Code      string   `dynamodbav:"code" json:"code"`
	Title     string   `dynamodbav:"title" json:"title"`
	Path      string   `dynamodbav:"path" json:"path"`
	Icon      string   `dynamodbav:"icon" json:"icon"`
	Order     int      `dynamodbav:"order" json:"order"`
	Active    bool     `dynamodbav:"active" json:"active"`
	Endpoints []string `dynamodbav:"endpoints" json:"endpoints"`
}
type Profile struct {
	PK          string `dynamodbav:"pk" json:"-"`
	SK          string `dynamodbav:"sk" json:"-"`
	Code        string `dynamodbav:"code" json:"code"`
	Title       string `dynamodbav:"title" json:"title"`
	Description string `dynamodbav:"description" json:"description"`
	Active      bool   `dynamodbav:"active" json:"active"`
}
type Permission struct {
	PK         string   `dynamodbav:"pk" json:"-"`
	SK         string   `dynamodbav:"sk" json:"-"`
	ModuleCode string   `dynamodbav:"moduleCode" json:"moduleCode"`
	Allowances []string `dynamodbav:"allowances" json:"allowances"`
}
type LoginAlias struct {
	PK     string `dynamodbav:"pk"`
	SK     string `dynamodbav:"sk"`
	UserID string `dynamodbav:"userId"`
}

func NormalizeLogin(v string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(v), ".", ""), "-", ""))
}
