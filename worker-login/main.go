package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	WorkerID     string `json:"workerId"`
	Username     string `json:"username"`
	District     string `json:"district"`
	FacilityID   string `json:"facilityId"`
	FacilityName string `json:"facilityName"`
	DemoAuth     bool   `json:"demoAuth"`
	Message      string `json:"message"`
}

type errorBody struct {
	Error string `json:"error"`
}

func main() {
	log.Printf("worker login lambda starting (demo-only auth)")
	lambda.Start(handler)
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var req loginRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		return jsonResponse(400, errorBody{Error: "username and password are required"})
	}

	worker, ok := AuthenticateDemoWorker(req.Username, req.Password)
	if !ok {
		log.Printf("demo login failed for username=%q", req.Username)
		return jsonResponse(401, errorBody{Error: "invalid demo credentials"})
	}

	log.Printf("demo login ok: workerId=%q district=%q facilityId=%q", worker.WorkerID, worker.District, worker.FacilityID)
	return jsonResponse(200, loginResponse{
		WorkerID:     worker.WorkerID,
		Username:     worker.Username,
		District:     worker.District,
		FacilityID:   worker.FacilityID,
		FacilityName: worker.FacilityName,
		DemoAuth:     true,
		Message:      "Demo worker login only — not production authentication",
	})
}

func jsonResponse(status int, body any) (events.APIGatewayProxyResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		log.Printf("failed to marshal response: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: 500,
			Headers: map[string]string{
				"Content-Type":                "application/json",
				"Access-Control-Allow-Origin": "*",
			},
			Body: `{"error":"internal error"}`,
		}, nil
	}
	return events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers: map[string]string{
			"Content-Type":                "application/json",
			"Access-Control-Allow-Origin": "*",
		},
		Body: string(b),
	}, nil
}
