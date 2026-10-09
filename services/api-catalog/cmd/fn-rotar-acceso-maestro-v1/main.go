package main
import("github.com/aws/aws-lambda-go/lambda"; master "ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions/masteraccess")
func main(){lambda.Start(master.Rotate)}
