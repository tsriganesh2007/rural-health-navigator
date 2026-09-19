package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const acknowledgedStatus = "acknowledged"

type ackRequest struct {
	SessionID string `json:"sessionId"`
}

type ackResponse struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

type errorBody struct {
	Error string `json:"error"`
}

// sessionAcknowledger updates triage session status (mocked in tests).
type sessionAcknowledger interface {
	Acknowledge(ctx context.Context, sessionID string) error
}

var (
	store              sessionAcknowledger
	errSessionNotFound = errors.New("session not found")
)

func main() {
	tableName := strings.TrimSpace(os.Getenv("TRIAGE_SESSIONS_TABLE_NAME"))
	if tableName == "" {
		log.Fatal("TRIAGE_SESSIONS_TABLE_NAME must be set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	store = &dynamoSessionStore{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}
	log.Printf("worker ack lambda starting: table=%q", tableName)
	lambda.Start(handler)
}

type dynamoSessionStore struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoSessionStore) Acknowledge(ctx context.Context, sessionID string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"sessionId": &types.AttributeValueMemberS{Value: sessionID},
		},
		ConditionExpression: aws.String("attribute_exists(sessionId)"),
		UpdateExpression:    aws.String("SET #status = :status"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: acknowledgedStatus},
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return errSessionNotFound
		}
		return err
	}
	return nil
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var req ackRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		log.Printf("validation failed: sessionId missing")
		return jsonResponse(400, errorBody{Error: "sessionId is required"})
	}

	log.Printf("worker ack: sessionId=%q", req.SessionID)

	if err := store.Acknowledge(ctx, req.SessionID); err != nil {
		if errors.Is(err, errSessionNotFound) {
			return jsonResponse(404, errorBody{Error: "session not found"})
		}
		log.Printf("session ack failed: sessionId=%q err=%v", req.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to acknowledge session"})
	}

	return jsonResponse(200, ackResponse{
		Message:   "session acknowledged",
		SessionID: req.SessionID,
		Status:    acknowledgedStatus,
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
