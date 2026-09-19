package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const queueLimit = 20

type queueSession struct {
	SessionID              string `json:"sessionId"`
	District               string `json:"district"`
	SymptomsText           string `json:"symptomsText"`
	Urgency                string `json:"urgency"`
	AdviceText             string `json:"adviceText"`
	CreatedAt              string `json:"createdAt"`
	ContactDoctorRequested bool   `json:"contactDoctorRequested"`
	Status                 string `json:"status,omitempty"`
}

type errorBody struct {
	Error string `json:"error"`
}

// sessionLister reads triage sessions for the worker queue (mocked in tests).
type sessionLister interface {
	ListSessions(ctx context.Context) ([]queueSession, error)
}

var store sessionLister

func main() {
	tableName := strings.TrimSpace(os.Getenv("TRIAGE_SESSIONS_TABLE_NAME"))
	if tableName == "" {
		log.Fatal("TRIAGE_SESSIONS_TABLE_NAME must be set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	store = &dynamoSessionLister{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}
	log.Printf("worker queue lambda starting: table=%q limit=%d", tableName, queueLimit)
	lambda.Start(handler)
}

type dynamoSessionLister struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoSessionLister) ListSessions(ctx context.Context) ([]queueSession, error) {
	sessions := make([]queueSession, 0)
	var startKey map[string]types.AttributeValue

	for {
		out, err := d.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(d.tableName),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, err
		}

		for _, item := range out.Items {
			sessions = append(sessions, queueSession{
				SessionID:              attrString(item, "sessionId"),
				District:               attrString(item, "district"),
				SymptomsText:           attrString(item, "symptomsText"),
				Urgency:                attrString(item, "urgency"),
				AdviceText:             attrString(item, "adviceText"),
				CreatedAt:              attrString(item, "createdAt"),
				ContactDoctorRequested: attrBool(item, "contactDoctorRequested"),
				Status:                 attrString(item, "status"),
			})
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}

	return sessions, nil
}

func attrString(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func attrBool(item map[string]types.AttributeValue, key string) bool {
	if v, ok := item[key].(*types.AttributeValueMemberBOOL); ok {
		return v.Value
	}
	// Backward-compatible default when older records omit the field.
	return false
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	sessions, err := store.ListSessions(ctx)
	if err != nil {
		log.Printf("worker queue scan failed: %v", err)
		return jsonResponse(500, errorBody{Error: "failed to load worker queue"})
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt > sessions[j].CreatedAt
	})

	if len(sessions) > queueLimit {
		sessions = sessions[:queueLimit]
	}
	if sessions == nil {
		sessions = []queueSession{}
	}

	log.Printf("worker queue returned: count=%d", len(sessions))
	return jsonResponse(200, sessions)
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
