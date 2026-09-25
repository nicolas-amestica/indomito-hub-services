package domain

type CountryOption struct {
	Code string `json:"code" dynamodbav:"code"`
	Name string `json:"name" dynamodbav:"name"`
}

type BankAccountOption struct {
	ID            string `json:"id" dynamodbav:"id"`
	Label         string `json:"label" dynamodbav:"label"`
	AccountNumber string `json:"accountNumber" dynamodbav:"accountNumber"`
	AccountHolder string `json:"accountHolder" dynamodbav:"accountHolder"`
	HolderDNI     string `json:"holderDNI" dynamodbav:"holderDNI"`
	Bank          string `json:"bank" dynamodbav:"bank"`
	AccountType   string `json:"accountType" dynamodbav:"accountType"`
	Email         string `json:"email" dynamodbav:"email"`
}

type ContractFormDefaults struct {
	DaysBeforePayment     int   `json:"daysBeforePayment" dynamodbav:"daysBeforePayment"`
	SpecialProgramDeposit int64 `json:"specialProgramDeposit" dynamodbav:"specialProgramDeposit"`
}

type ContractFormConfiguration struct {
	CompanyRepresentatives []Person             `json:"companyRepresentatives" dynamodbav:"companyRepresentatives"`
	BankAccounts           []BankAccountOption  `json:"bankAccounts" dynamodbav:"bankAccounts"`
	Defaults               ContractFormDefaults `json:"defaults" dynamodbav:"defaults"`
	Countries              []CountryOption      `json:"countries" dynamodbav:"countries"`
}

type ContractFormConfigurationItem struct {
	PK          string         `json:"pk" dynamodbav:"pk"`
	SK          string         `json:"sk" dynamodbav:"sk"`
	Content     map[string]any `json:"content" dynamodbav:"content"`
	Description string         `json:"description" dynamodbav:"description"`
	ID          string         `json:"id" dynamodbav:"id"`
	Scope       string         `json:"scope" dynamodbav:"scope"`
}

const ContractFormConfigurationPK = "APP#INDOMITO_HUB#CFG#FRM#01KQWC0R139GW9481BEYN55RCP"
const ContractFormConfigurationSKPrefix = "FRM#SCP#"
