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

const (
	queueLimit         = 50
	pendingStatus      = "pending"
	acknowledgedStatus = "acknowledged"
)

type queueSession struct {
	SessionID              string `json:"sessionId"`
	District               string `json:"district"`
	FacilityID             string `json:"facilityId,omitempty"`
	FacilityName           string `json:"facilityName,omitempty"`
	SymptomsText           string `json:"symptomsText"`
	Urgency                string `json:"urgency"`
	AdviceText             string `json:"adviceText"`
	CreatedAt              string `json:"createdAt"`
	ContactDoctorRequested bool   `json:"contactDoctorRequested"`
	ContactNumber          string `json:"contactNumber,omitempty"`
	Status                 string `json:"status"`
	AcknowledgedBy         string `json:"acknowledgedBy,omitempty"`
	AcknowledgedAt         string `json:"acknowledgedAt,omitempty"`
}

type errorBody struct {
	Error string `json:"error"`
}

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
			status := attrString(item, "status")
			if status == "" {
				status = pendingStatus
			}
			sessions = append(sessions, queueSession{
				SessionID:              attrString(item, "sessionId"),
				District:               attrString(item, "district"),
				FacilityID:             attrString(item, "facilityId"),
				FacilityName:           attrString(item, "facilityName"),
				SymptomsText:           attrString(item, "symptomsText"),
				Urgency:                attrString(item, "urgency"),
				AdviceText:             attrString(item, "adviceText"),
				CreatedAt:              attrString(item, "createdAt"),
				ContactDoctorRequested: attrBool(item, "contactDoctorRequested"),
				ContactNumber:          attrString(item, "contactNumber"),
				Status:                 status,
				AcknowledgedBy:         attrString(item, "acknowledgedBy"),
				AcknowledgedAt:         attrString(item, "acknowledgedAt"),
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
	return false
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	workerID := strings.TrimSpace(request.QueryStringParameters["workerId"])
	if workerID == "" {
		return jsonResponse(400, errorBody{Error: "workerId query parameter is required"})
	}

	worker, ok := FindWorkerByID(workerID)
	if !ok {
		log.Printf("unknown workerId=%q", workerID)
		return jsonResponse(404, errorBody{Error: "worker not found"})
	}

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		log.Printf("worker queue scan failed: %v", err)
		return jsonResponse(500, errorBody{Error: "failed to load worker queue"})
	}

	filtered := filterSessionsForWorker(sessions, worker)
	ordered := orderQueueSessions(filtered)

	if len(ordered) > queueLimit {
		ordered = ordered[:queueLimit]
	}
	if ordered == nil {
		ordered = []queueSession{}
	}

	log.Printf("worker queue returned: workerId=%q facilityId=%q count=%d", worker.WorkerID, worker.FacilityID, len(ordered))
	return jsonResponse(200, ordered)
}

func filterSessionsForWorker(sessions []queueSession, worker DemoWorker) []queueSession {
	out := make([]queueSession, 0)
	for _, s := range sessions {
		if sessionBelongsToWorker(s, worker) {
			out = append(out, s)
		}
	}
	return out
}

func sessionBelongsToWorker(s queueSession, worker DemoWorker) bool {
	if s.FacilityID != "" {
		return s.FacilityID == worker.FacilityID
	}
	// Backward compatible: older sessions without facilityId match by district.
	return strings.EqualFold(strings.TrimSpace(s.District), worker.District)
}

func orderQueueSessions(sessions []queueSession) []queueSession {
	pending := make([]queueSession, 0)
	acked := make([]queueSession, 0)
	for _, s := range sessions {
		if strings.EqualFold(s.Status, acknowledgedStatus) {
			acked = append(acked, s)
		} else {
			if s.Status == "" {
				s.Status = pendingStatus
			}
			pending = append(pending, s)
		}
	}
	sortNewestFirst(pending)
	sortNewestFirst(acked)
	return append(pending, acked...)
}

func sortNewestFirst(sessions []queueSession) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt > sessions[j].CreatedAt
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
