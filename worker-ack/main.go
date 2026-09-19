package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	pendingStatus      = "pending"
	acknowledgedStatus = "acknowledged"
)

type ackRequest struct {
	SessionID string `json:"sessionId"`
	WorkerID  string `json:"workerId"`
}

type ackResponse struct {
	Message        string `json:"message"`
	SessionID      string `json:"sessionId"`
	Status         string `json:"status"`
	AcknowledgedBy string `json:"acknowledgedBy"`
	AcknowledgedAt string `json:"acknowledgedAt"`
}

type errorBody struct {
	Error string `json:"error"`
}

type sessionSnapshot struct {
	SessionID  string
	District   string
	FacilityID string
	Status     string
}

type sessionAcknowledger interface {
	GetSession(ctx context.Context, sessionID string) (sessionSnapshot, error)
	Acknowledge(ctx context.Context, sessionID, workerID, acknowledgedAt string) error
}

var (
	store                  sessionAcknowledger
	nowUTC                 = func() time.Time { return time.Now().UTC() }
	errSessionNotFound     = errors.New("session not found")
	errAlreadyAcknowledged = errors.New("session already acknowledged")
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

func (d *dynamoSessionStore) GetSession(ctx context.Context, sessionID string) (sessionSnapshot, error) {
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
	status := attrString(out.Item, "status")
	if status == "" {
		status = pendingStatus
	}
	return sessionSnapshot{
		SessionID:  attrString(out.Item, "sessionId"),
		District:   attrString(out.Item, "district"),
		FacilityID: attrString(out.Item, "facilityId"),
		Status:     status,
	}, nil
}

func (d *dynamoSessionStore) Acknowledge(ctx context.Context, sessionID, workerID, acknowledgedAt string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"sessionId": &types.AttributeValueMemberS{Value: sessionID},
		},
		ConditionExpression: aws.String("attribute_exists(sessionId) AND (attribute_not_exists(#status) OR #status = :pending)"),
		UpdateExpression:    aws.String("SET #status = :acked, acknowledgedBy = :by, acknowledgedAt = :at"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending": &types.AttributeValueMemberS{Value: pendingStatus},
			":acked":   &types.AttributeValueMemberS{Value: acknowledgedStatus},
			":by":      &types.AttributeValueMemberS{Value: workerID},
			":at":      &types.AttributeValueMemberS{Value: acknowledgedAt},
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			// Distinguish missing vs already acknowledged with a GetItem.
			snap, getErr := d.GetSession(ctx, sessionID)
			if getErr != nil {
				if errors.Is(getErr, errSessionNotFound) {
					return errSessionNotFound
				}
				return getErr
			}
			if strings.EqualFold(snap.Status, acknowledgedStatus) {
				return errAlreadyAcknowledged
			}
			return errAlreadyAcknowledged
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

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var req ackRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.SessionID = strings.TrimSpace(req.SessionID)
	req.WorkerID = strings.TrimSpace(req.WorkerID)
	if req.SessionID == "" || req.WorkerID == "" {
		return jsonResponse(400, errorBody{Error: "sessionId and workerId are required"})
	}

	worker, ok := FindWorkerByID(req.WorkerID)
	if !ok {
		return jsonResponse(404, errorBody{Error: "worker not found"})
	}

	snap, err := store.GetSession(ctx, req.SessionID)
	if err != nil {
		if errors.Is(err, errSessionNotFound) {
			return jsonResponse(404, errorBody{Error: "session not found"})
		}
		log.Printf("session load failed: sessionId=%q err=%v", req.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to load session"})
	}

	if !sessionAssignableToWorker(snap, worker) {
		log.Printf("ack rejected wrong facility: sessionId=%q workerFacility=%q sessionFacility=%q",
			req.SessionID, worker.FacilityID, snap.FacilityID)
		return jsonResponse(403, errorBody{Error: "session is not assigned to this worker's facility"})
	}

	if strings.EqualFold(snap.Status, acknowledgedStatus) {
		return jsonResponse(409, errorBody{Error: "session already acknowledged"})
	}

	ackedAt := nowUTC().Format(time.RFC3339)
	log.Printf("worker ack: sessionId=%q workerId=%q", req.SessionID, worker.WorkerID)

	if err := store.Acknowledge(ctx, req.SessionID, worker.WorkerID, ackedAt); err != nil {
		if errors.Is(err, errSessionNotFound) {
			return jsonResponse(404, errorBody{Error: "session not found"})
		}
		if errors.Is(err, errAlreadyAcknowledged) {
			return jsonResponse(409, errorBody{Error: "session already acknowledged"})
		}
		log.Printf("session ack failed: sessionId=%q err=%v", req.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to acknowledge session"})
	}

	return jsonResponse(200, ackResponse{
		Message:        "session acknowledged",
		SessionID:      req.SessionID,
		Status:         acknowledgedStatus,
		AcknowledgedBy: worker.WorkerID,
		AcknowledgedAt: ackedAt,
	})
}

func sessionAssignableToWorker(snap sessionSnapshot, worker DemoWorker) bool {
	if snap.FacilityID != "" {
		return snap.FacilityID == worker.FacilityID
	}
	return strings.EqualFold(strings.TrimSpace(snap.District), worker.District)
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
