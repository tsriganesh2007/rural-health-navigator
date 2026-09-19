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

type workerStatusRequest struct {
	FacilityID        string `json:"facilityId"`
	StatusOpen        *bool  `json:"statusOpen"`
	StatusHasDoctor   *bool  `json:"statusHasDoctor"`
	StatusHasMedicine *bool  `json:"statusHasMedicine"`
	LastUpdatedBy     string `json:"lastUpdatedBy"`
}

type workerStatusResponse struct {
	Message       string `json:"message"`
	FacilityID    string `json:"facilityId"`
	LastUpdatedAt string `json:"lastUpdatedAt"`
}

type errorBody struct {
	Error string `json:"error"`
}

// facilityStatusStore updates facility status fields in DynamoDB (mocked in tests).
type facilityStatusStore interface {
	UpdateStatus(ctx context.Context, facilityID string, statusOpen, statusHasDoctor, statusHasMedicine bool, lastUpdatedBy, lastUpdatedAt string) error
}

var (
	store     facilityStatusStore
	tableName string
	nowUTC    = func() time.Time { return time.Now().UTC() }
)

var errFacilityNotFound = errors.New("facility not found")

func main() {
	tableName = strings.TrimSpace(os.Getenv("FACILITIES_TABLE_NAME"))
	if tableName == "" {
		log.Fatal("FACILITIES_TABLE_NAME must be set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	store = &dynamoFacilityStore{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}
	log.Printf("worker status lambda starting: table=%q", tableName)
	lambda.Start(handler)
}

type dynamoFacilityStore struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoFacilityStore) UpdateStatus(ctx context.Context, facilityID string, statusOpen, statusHasDoctor, statusHasMedicine bool, lastUpdatedBy, lastUpdatedAt string) error {
	_, err := d.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(d.tableName),
		Key: map[string]types.AttributeValue{
			"facilityId": &types.AttributeValueMemberS{Value: facilityID},
		},
		ConditionExpression: aws.String("attribute_exists(facilityId)"),
		UpdateExpression: aws.String(
			"SET statusOpen = :open, statusHasDoctor = :doctor, statusHasMedicine = :medicine, lastUpdatedBy = :by, lastUpdatedAt = :at",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":open":     &types.AttributeValueMemberBOOL{Value: statusOpen},
			":doctor":   &types.AttributeValueMemberBOOL{Value: statusHasDoctor},
			":medicine": &types.AttributeValueMemberBOOL{Value: statusHasMedicine},
			":by":       &types.AttributeValueMemberS{Value: lastUpdatedBy},
			":at":       &types.AttributeValueMemberS{Value: lastUpdatedAt},
		},
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return errFacilityNotFound
		}
		return err
	}
	return nil
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var req workerStatusRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.FacilityID = strings.TrimSpace(req.FacilityID)
	req.LastUpdatedBy = strings.TrimSpace(req.LastUpdatedBy)

	if req.FacilityID == "" || req.LastUpdatedBy == "" ||
		req.StatusOpen == nil || req.StatusHasDoctor == nil || req.StatusHasMedicine == nil {
		log.Printf("validation failed: facilityIdSet=%t lastUpdatedBySet=%t statusFieldsPresent=%t",
			req.FacilityID != "", req.LastUpdatedBy != "",
			req.StatusOpen != nil && req.StatusHasDoctor != nil && req.StatusHasMedicine != nil)
		return jsonResponse(400, errorBody{Error: "facilityId, statusOpen, statusHasDoctor, statusHasMedicine, and lastUpdatedBy are required"})
	}

	lastUpdatedAt := nowUTC().Format(time.RFC3339)
	log.Printf("worker status update: facilityId=%q lastUpdatedBy=%q", req.FacilityID, req.LastUpdatedBy)

	err := store.UpdateStatus(ctx, req.FacilityID, *req.StatusOpen, *req.StatusHasDoctor, *req.StatusHasMedicine, req.LastUpdatedBy, lastUpdatedAt)
	if err != nil {
		if errors.Is(err, errFacilityNotFound) {
			return jsonResponse(404, errorBody{Error: "facility not found"})
		}
		log.Printf("facility status update failed: %v", err)
		return jsonResponse(500, errorBody{Error: "failed to update facility status"})
	}

	return jsonResponse(200, workerStatusResponse{
		Message:       "status updated",
		FacilityID:    req.FacilityID,
		LastUpdatedAt: lastUpdatedAt,
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
