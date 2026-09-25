package functions

import "ind-hub-api-gox-sls-pri-gh/bootstrap"

type Config struct {
	ProgramsTableName   string
	CatalogsTableName   string
	DocumentsBucketName string
	FunctionName        string
	Port                string
}

func LoadConfig(base bootstrap.Config) Config {
	return Config{ProgramsTableName: bootstrap.GetRequiredEnv("PROGRAMS_TABLE_NAME"), CatalogsTableName: bootstrap.GetRequiredEnv("CATALOGS_TABLE_NAME"), DocumentsBucketName: bootstrap.GetRequiredEnv("DOCUMENTS_BUCKET_NAME"), FunctionName: bootstrap.GetEnv("APP_FUNCTION_NAME", base.AppName), Port: base.Port}
}
