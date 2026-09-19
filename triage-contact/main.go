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
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

type contactRequest struct {
	SessionID string `json:"sessionId"`
}

type contactResponse struct {
	Message                string `json:"message"`
	SessionID              string `json:"sessionId"`
	Urgency                string `json:"urgency"`
	ContactDoctorRequested bool   `json:"contactDoctorRequested"`
}

type errorBody struct {
	Error string `json:"error"`
}

type sessionSnapshot struct {
	SessionID string
	District  string
	Urgency   string
}

type triageNotification struct {
	SessionID              string `json:"sessionId"`
	District               string `json:"district"`
	Urgency                string `json:"urgency"`
	ContactDoctorRequested bool   `json:"contactDoctorRequested"`
}

// contactStore loads a session and marks doctor contact requested (mocked in tests).
type contactStore interface {
	GetSession(ctx context.Context, sessionID string) (sessionSnapshot, error)
	MarkContactRequested(ctx context.Context, sessionID string) error
}

// notificationPublisher publishes triage notifications (mocked in tests).
type notificationPublisher interface {
	PublishTriageNotification(ctx context.Context, note triageNotification) error
}

var (
	sessions           contactStore
	notifier           notificationPublisher
	errSessionNotFound = errors.New("session not found")
)

func main() {
	tableName := strings.TrimSpace(os.Getenv("TRIAGE_SESSIONS_TABLE_NAME"))
	if tableName == "" {
		log.Fatal("TRIAGE_SESSIONS_TABLE_NAME must be set")
	}

	topicARN := strings.TrimSpace(os.Getenv("TRIAGE_NOTIFICATIONS_TOPIC_ARN"))
	if topicARN == "" {
		log.Fatal("TRIAGE_NOTIFICATIONS_TOPIC_ARN must be set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	sessions = &dynamoContactStore{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}
	notifier = &snsNotifier{
		client:   sns.NewFromConfig(cfg),
		topicARN: topicARN,
	}

	log.Printf("triage contact lambda starting: table=%q", tableName)
	lambda.Start(handler)
}

type dynamoContactStore struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoContactStore) GetSession(ctx context.Context, sessionID string) (sessionSnapshot, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"sessionId": &types.AttributeValueMemberS{Value: sessionID},
		},
	})
	if err != nil {
		return sessionSnapshot{}, err
	}
	if out.Item == nil || len(out.Item) == 0 {
		return sessionSnapshot{}, errSessionNotFound
	}
	return sessionSnapshot{
		SessionID: attrString(out.Item, "sessionId"),
		District:  attrString(out.Item, "district"),
		Urgency:   attrString(out.Item, "urgency"),
	}, nil
}

func (d *dynamoContactStore) MarkContactRequested(ctx context.Context, sessionID string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"sessionId": &types.AttributeValueMemberS{Value: sessionID},
		},
		ConditionExpression: aws.String("attribute_exists(sessionId)"),
		UpdateExpression:    aws.String("SET contactDoctorRequested = :requested"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":requested": &types.AttributeValueMemberBOOL{Value: true},
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

func attrString(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

type snsNotifier struct {
	client   *sns.Client
	topicARN string
}

func (n *snsNotifier) PublishTriageNotification(ctx context.Context, note triageNotification) error {
	body, err := json.Marshal(note)
	if err != nil {
		return err
	}
	_, err = n.client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(n.topicARN),
		Message:  aws.String(string(body)),
	})
	return err
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var req contactRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		log.Printf("validation failed: sessionId missing")
		return jsonResponse(400, errorBody{Error: "sessionId is required"})
	}

	log.Printf("triage contact request: sessionId=%q", req.SessionID)

	snap, err := sessions.GetSession(ctx, req.SessionID)
	if err != nil {
		if errors.Is(err, errSessionNotFound) {
			return jsonResponse(404, errorBody{Error: "session not found"})
		}
		log.Printf("session load failed: sessionId=%q err=%v", req.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to load triage session"})
	}

	if strings.EqualFold(snap.Urgency, "emergency") {
		return jsonResponse(400, errorBody{Error: "emergency sessions should seek emergency care immediately; contact request not applicable"})
	}

	if err := sessions.MarkContactRequested(ctx, req.SessionID); err != nil {
		if errors.Is(err, errSessionNotFound) {
			return jsonResponse(404, errorBody{Error: "session not found"})
		}
		log.Printf("contact mark failed: sessionId=%q err=%v", req.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to save contact request"})
	}

	note := triageNotification{
		SessionID:              snap.SessionID,
		District:               snap.District,
		Urgency:                snap.Urgency,
		ContactDoctorRequested: true,
	}
	if err := notifier.PublishTriageNotification(ctx, note); err != nil {
		log.Printf("contact notification publish failed: sessionId=%q err=%v", req.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to publish contact notification"})
	}

	return jsonResponse(200, contactResponse{
		Message:                "doctor contact requested",
		SessionID:              snap.SessionID,
		Urgency:                snap.Urgency,
		ContactDoctorRequested: true,
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
