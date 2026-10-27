package domain

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const agentManagementPK = "AGENT_CONFIG"

type AgentPersonality struct {
	DisplayName            string   `json:"displayName" dynamodbav:"displayName"`
	Role                   string   `json:"role" dynamodbav:"role"`
	Tone                   string   `json:"tone" dynamodbav:"tone"`
	Language               string   `json:"language" dynamodbav:"language"`
	ResponseStyle          string   `json:"responseStyle" dynamodbav:"responseStyle"`
	UseEmoji               bool     `json:"useEmoji" dynamodbav:"useEmoji"`
	Greeting               string   `json:"greeting" dynamodbav:"greeting"`
	Closing                string   `json:"closing" dynamodbav:"closing"`
	ForbiddenPhrases       []string `json:"forbiddenPhrases" dynamodbav:"forbiddenPhrases"`
	AdditionalInstructions string   `json:"additionalInstructions" dynamodbav:"additionalInstructions"`
}

type AgentGenerationSettings struct {
	Model                  string  `json:"model" dynamodbav:"model"`
	Temperature            float64 `json:"temperature" dynamodbav:"temperature"`
	TopP                   float64 `json:"topP" dynamodbav:"topP"`
	MaxMessageLength       int     `json:"maxMessageLength" dynamodbav:"maxMessageLength"`
	MaxHistoryTurns        int     `json:"maxHistoryTurns" dynamodbav:"maxHistoryTurns"`
	DebounceMS             int     `json:"debounceMs" dynamodbav:"debounceMs"`
	RateLimitMax           int     `json:"rateLimitMax" dynamodbav:"rateLimitMax"`
	RateLimitWindowSeconds int     `json:"rateLimitWindowSeconds" dynamodbav:"rateLimitWindowSeconds"`
}

type AgentPolicies struct {
	OfferMeetings              bool   `json:"offerMeetings" dynamodbav:"offerMeetings"`
	HumanHandoffEnabled        bool   `json:"humanHandoffEnabled" dynamodbav:"humanHandoffEnabled"`
	HandoffWhatsAppEnabled     bool   `json:"handoffWhatsAppEnabled" dynamodbav:"handoffWhatsAppEnabled"`
	HandoffEmailEnabled        bool   `json:"handoffEmailEnabled" dynamodbav:"handoffEmailEnabled"`
	HandoffPhone               string `json:"handoffPhone" dynamodbav:"handoffPhone"`
	HandoffEmail               string `json:"handoffEmail" dynamodbav:"handoffEmail"`
	ExplainProgramDifferences  bool   `json:"explainProgramDifferences" dynamodbav:"explainProgramDifferences"`
	SendSernaturWithQuotations bool   `json:"sendSernaturWithQuotations" dynamodbav:"sendSernaturWithQuotations"`
	HandoffConfirmation        string `json:"handoffConfirmation" dynamodbav:"handoffConfirmation"`
	FallbackResponse           string `json:"fallbackResponse" dynamodbav:"fallbackResponse"`
}

type AgentKnowledgeEntry struct {
	ID            string   `json:"id" dynamodbav:"id"`
	Title         string   `json:"title" dynamodbav:"title"`
	Category      string   `json:"category" dynamodbav:"category"`
	QuestionHints []string `json:"questionHints" dynamodbav:"questionHints"`
	Answer        string   `json:"answer" dynamodbav:"answer"`
	Active        bool     `json:"active" dynamodbav:"active"`
	Priority      int      `json:"priority" dynamodbav:"priority"`
	RequiresHuman bool     `json:"requiresHuman" dynamodbav:"requiresHuman"`
}

type AgentProgram struct {
	ID            string   `json:"id" dynamodbav:"id"`
	Name          string   `json:"name" dynamodbav:"name"`
	Destination   string   `json:"destination" dynamodbav:"destination"`
	Country       string   `json:"country" dynamodbav:"country"`
	OriginRegions []string `json:"originRegions" dynamodbav:"originRegions"`
	OriginCities  []string `json:"originCities" dynamodbav:"originCities"`
	Description   string   `json:"description" dynamodbav:"description"`
	Activities    []string `json:"activities" dynamodbav:"activities"`
	DurationDays  int      `json:"durationDays" dynamodbav:"durationDays"`
	DocumentKey   string   `json:"documentKey" dynamodbav:"documentKey"`
	FileName      string   `json:"fileName" dynamodbav:"fileName"`
	Active        bool     `json:"active" dynamodbav:"active"`
	SortOrder     int      `json:"sortOrder" dynamodbav:"sortOrder"`
}

type AgentManagedConfig struct {
	Personality AgentPersonality        `json:"personality" dynamodbav:"personality"`
	Generation  AgentGenerationSettings `json:"generation" dynamodbav:"generation"`
	Policies    AgentPolicies           `json:"policies" dynamodbav:"policies"`
	Knowledge   []AgentKnowledgeEntry   `json:"knowledge" dynamodbav:"knowledge"`
	Programs    []AgentProgram          `json:"programs" dynamodbav:"programs"`
}

type AgentConfigRevision struct {
	PK          string             `json:"-" dynamodbav:"pk"`
	SK          string             `json:"-" dynamodbav:"sk"`
	Version     string             `json:"version" dynamodbav:"version"`
	Status      string             `json:"status" dynamodbav:"status"`
	UpdatedAt   time.Time          `json:"updatedAt" dynamodbav:"updatedAt"`
	PublishedAt *time.Time         `json:"publishedAt,omitempty" dynamodbav:"publishedAt,omitempty"`
	Config      AgentManagedConfig `json:"config" dynamodbav:"config"`
}

type AgentManagementSnapshot struct {
	Draft     AgentConfigRevision   `json:"draft"`
	Published *AgentConfigRevision  `json:"published"`
	Versions  []AgentConfigRevision `json:"versions"`
}

type agentConfigDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type AgentManagementService struct {
	db    agentConfigDynamo
	table string
	now   func() time.Time
}

func NewAgentManagementService(db agentConfigDynamo, table string) (*AgentManagementService, error) {
	if db == nil || table == "" {
		return nil, errors.New("agent management configuration is required")
	}
	return &AgentManagementService{db: db, table: table, now: time.Now}, nil
}

func DefaultAgentManagedConfig() AgentManagedConfig {
	return AgentManagedConfig{
		Personality: AgentPersonality{DisplayName: "Agente IA", Role: "Asistente virtual de Giras Indomito", Tone: "Amigable y profesional", Language: "Espanol chileno formal", ResponseStyle: "Preciso, conciso y directo", Greeting: "Hola, soy el Agente IA de Giras Indomito. Como puedo ayudarte?", ForbiddenPhrases: []string{"descuento por pago en efectivo", "Susana Guevara"}},
		Generation:  AgentGenerationSettings{Model: "gemini-3.5-flash-lite", Temperature: .2, TopP: .8, MaxMessageLength: 1200, MaxHistoryTurns: 20, DebounceMS: 4000, RateLimitMax: 12, RateLimitWindowSeconds: 60},
		Policies:    AgentPolicies{OfferMeetings: true, HumanHandoffEnabled: true, HandoffWhatsAppEnabled: true, HandoffEmailEnabled: true, HandoffPhone: "56963552893", HandoffEmail: "info@girasindomito.cl", ExplainProgramDifferences: true, SendSernaturWithQuotations: true, HandoffConfirmation: "Te derive con nuestro equipo para que continuen ayudandote. Te contactaran apenas sea posible.", FallbackResponse: "No tengo esa informacion confirmada. Puedo derivarte con nuestro equipo."},
		Knowledge: []AgentKnowledgeEntry{
			{ID: "destinos", Title: "Destinos y origen", Category: "Programas", Answer: "Los destinos principales son Bariloche, Camboriu y circuitos del Sur de Chile. Brasil corresponde a Camboriu. Para Bariloche se usa la region chilena real del colegio y los circuitos del Sur disponibles por PDF aplican a origen V Region.", Active: true, Priority: 100},
			{ID: "financiamiento", Title: "Financiamiento y beneficios", Category: "Pagos", Answer: "Hay tres talonarios por persona; cada numero cuesta $1.000, los premios los aporta Giras Indomito y lo recaudado puede financiar el viaje. Las cuotas las define el curso y no tienen interes. No existe descuento por pago en efectivo. Hay 10% para quien tenga un hermano que haya viajado antes con la empresa.", Active: true, Priority: 90},
			{ID: "documentos", Title: "Documentos para viajar", Category: "Viaje", Answer: "Mayores de 18 requieren cedula vigente. Menores requieren autorizacion notarial de ambos padres, certificado de nacimiento y cedula vigente; aplica certificado de defuncion o autorizacion del tribunal de familia cuando corresponda.", Active: true, Priority: 80},
			{ID: "confianza", Title: "Registro Sernatur", Category: "Empresa", Answer: "Giras Indomito esta inscrita en el Registro Nacional de Prestadores de Servicios Turisticos de Sernatur.", Active: true, Priority: 70},
		}, Programs: []AgentProgram{},
	}
}

func validateManagedConfig(c AgentManagedConfig) error {
	if strings.TrimSpace(c.Personality.DisplayName) == "" || len([]rune(c.Personality.AdditionalInstructions)) > 6000 {
		return errors.New("invalid personality configuration")
	}
	if c.Generation.Temperature < 0 || c.Generation.Temperature > 1 || c.Generation.TopP < 0 || c.Generation.TopP > 1 || c.Generation.MaxMessageLength < 200 || c.Generation.MaxMessageLength > 4000 || c.Generation.MaxHistoryTurns < 1 || c.Generation.MaxHistoryTurns > 50 || c.Generation.DebounceMS < 0 || c.Generation.DebounceMS > 30000 {
		return errors.New("invalid generation configuration")
	}
	allowedModels := map[string]bool{"gemini-3.5-flash-lite": true, "gemini-2.5-flash-lite": true, "gemini-2.5-flash": true}
	if !allowedModels[c.Generation.Model] {
		return errors.New("model is not allowed")
	}
	if len(c.Knowledge) > 200 || len(c.Programs) > 100 {
		return errors.New("configuration collection limit exceeded")
	}
	if c.Policies.HumanHandoffEnabled {
		phone := strings.TrimPrefix(strings.TrimSpace(c.Policies.HandoffPhone), "+")
		if c.Policies.HandoffWhatsAppEnabled && !derivationNotificationPhonePattern.MatchString(phone) {
			return errors.New("invalid handoff phone")
		}
		if c.Policies.HandoffEmailEnabled {
			address, err := mail.ParseAddress(strings.TrimSpace(c.Policies.HandoffEmail))
			if err != nil || address.Address != strings.TrimSpace(c.Policies.HandoffEmail) {
				return errors.New("invalid handoff email")
			}
		}
		if !c.Policies.HandoffWhatsAppEnabled && !c.Policies.HandoffEmailEnabled {
			return errors.New("handoff requires at least one notification channel")
		}
	}
	for _, item := range c.Knowledge {
		if item.ID == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Answer) == "" {
			return errors.New("invalid knowledge entry")
		}
	}
	for _, item := range c.Programs {
		if item.ID == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Destination) == "" {
			return errors.New("invalid program")
		}
	}
	return nil
}

func (s *AgentManagementService) get(ctx context.Context, sk string) (*AgentConfigRevision, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: agentManagementPK}, "sk": &types.AttributeValueMemberS{Value: sk}}, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("read agent config: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var result AgentConfigRevision
	if err = attributevalue.UnmarshalMap(out.Item, &result); err != nil {
		return nil, fmt.Errorf("decode agent config: %w", err)
	}
	return &result, nil
}

func (s *AgentManagementService) Snapshot(ctx context.Context) (AgentManagementSnapshot, error) {
	draft, err := s.get(ctx, "DRAFT")
	if err != nil {
		return AgentManagementSnapshot{}, err
	}
	published, err := s.get(ctx, "PUBLISHED")
	if err != nil {
		return AgentManagementSnapshot{}, err
	}
	if draft == nil {
		draft = &AgentConfigRevision{Version: "draft", Status: "draft", UpdatedAt: s.now().UTC(), Config: DefaultAgentManagedConfig()}
	}
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{TableName: &s.table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :version)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: agentManagementPK}, ":version": &types.AttributeValueMemberS{Value: "VERSION#"}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(10)})
	if err != nil {
		return AgentManagementSnapshot{}, fmt.Errorf("list agent config versions: %w", err)
	}
	var versions []AgentConfigRevision
	if err = attributevalue.UnmarshalListOfMaps(out.Items, &versions); err != nil {
		return AgentManagementSnapshot{}, err
	}
	return AgentManagementSnapshot{Draft: *draft, Published: published, Versions: versions}, nil
}

func (s *AgentManagementService) SaveDraft(ctx context.Context, config AgentManagedConfig) (AgentConfigRevision, error) {
	if err := validateManagedConfig(config); err != nil {
		return AgentConfigRevision{}, err
	}
	revision := AgentConfigRevision{PK: agentManagementPK, SK: "DRAFT", Version: "draft", Status: "draft", UpdatedAt: s.now().UTC(), Config: config}
	item, err := attributevalue.MarshalMap(revision)
	if err != nil {
		return AgentConfigRevision{}, err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: item})
	return revision, err
}

func (s *AgentManagementService) Publish(ctx context.Context) (AgentConfigRevision, error) {
	draft, err := s.get(ctx, "DRAFT")
	if err != nil {
		return AgentConfigRevision{}, err
	}
	if draft == nil {
		return AgentConfigRevision{}, errors.New("draft does not exist")
	}
	if err = validateManagedConfig(draft.Config); err != nil {
		return AgentConfigRevision{}, err
	}
	now := s.now().UTC()
	version := now.Format("20060102T150405.000000000Z")
	published := AgentConfigRevision{PK: agentManagementPK, SK: "PUBLISHED", Version: version, Status: "published", UpdatedAt: now, PublishedAt: &now, Config: draft.Config}
	historical := published
	historical.SK = "VERSION#" + version
	pubItem, _ := attributevalue.MarshalMap(published)
	historyItem, _ := attributevalue.MarshalMap(historical)
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Put: &types.Put{TableName: &s.table, Item: pubItem}}, {Put: &types.Put{TableName: &s.table, Item: historyItem}}}})
	return published, err
}

func (s *AgentManagementService) Restore(ctx context.Context, version string) (AgentConfigRevision, error) {
	revision, err := s.get(ctx, "VERSION#"+version)
	if err != nil {
		return AgentConfigRevision{}, err
	}
	if revision == nil {
		return AgentConfigRevision{}, errors.New("version does not exist")
	}
	_, err = s.SaveDraft(ctx, revision.Config)
	if err != nil {
		return AgentConfigRevision{}, err
	}
	return s.Publish(ctx)
}

func (s *AgentManagementService) Published(ctx context.Context) (*AgentManagedConfig, error) {
	revision, err := s.get(ctx, "PUBLISHED")
	if err != nil || revision == nil {
		return nil, err
	}
	return &revision.Config, nil
}
