package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-meet/functions"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/reuniones", serve)
	mux.HandleFunc("/v1/reuniones/", serve)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}
	server := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("api-meet listening on :%s", port)
	log.Fatal(server.ListenAndServe())
}

func serve(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var body any
	if r.ContentLength > (1 << 20) {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
	}
	var payload string
	if body != nil {
		data, _ := json.Marshal(body)
		payload = string(data)
	}
	pathParameters := map[string]string{}
	if value := strings.TrimPrefix(r.URL.Path, "/v1/reuniones/"); value != r.URL.Path {
		pathParameters["eventId"] = value
	}
	query := map[string]string{}
	for key := range r.URL.Query() {
		query[key] = r.URL.Query().Get(key)
	}
	response, err := functions.Handle(context.Background(), events.APIGatewayV2HTTPRequest{
		Body: payload,
		PathParameters: pathParameters,
		QueryStringParameters: query,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: r.Method},
		},
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write([]byte(response.Body))
}
