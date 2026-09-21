package main

import (
	"github.com/aws/aws-lambda-go/lambda"

	favorites "ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions/crear-favorito-v1"
)

func main() {
	lambda.Start(favorites.Handle)
}
