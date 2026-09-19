package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"google.golang.org/genai"
)

const (
	defaultGeminiModelID = "gemini-3.5-flash-lite"
	apiKeyPlaceholder    = "REPLACE_WITH_GEMINI_API_KEY"
	pendingStatus        = "pending"
)

const systemPrompt = `You are a non-diagnostic triage assistant for rural health guidance.
You must NOT claim to diagnose any disease.
Classify urgency into exactly one of: self_care, visit_soon, emergency.
Provide concise safety-focused advice.
For emergency situations, direct the user to seek urgent or emergency care immediately.
Do not invent facilities, doctors, medicines, or medical facts.

Respond with JSON matching the required schema only.`

var validUrgencies = map[string]bool{
	"self_care":  true,
	"visit_soon": true,
	"emergency":  true,
}

var phoneDigitsPattern = regexp.MustCompile(`^\+?[0-9][0-9\s\-()]{5,18}[0-9]$`)

type triageRequest struct {
	SymptomsText           string `json:"symptomsText"`
	District               string `json:"district"`
	ContactNumber          string `json:"contactNumber,omitempty"`
	ContactDoctorRequested *bool  `json:"contactDoctorRequested,omitempty"`
}

type triageResponse struct {
	SessionID              string `json:"sessionId"`
	Urgency                string `json:"urgency"`
	AdviceText             string `json:"adviceText"`
	ContactDoctorRequested bool   `json:"contactDoctorRequested"`
	FacilityID             string `json:"facilityId,omitempty"`
	FacilityName           string `json:"facilityName,omitempty"`
	FacilityAvailable      bool   `json:"facilityAvailable"`
	FacilityMessage        string `json:"facilityMessage"`
	District               string `json:"district"`
}

type modelTriageResult struct {
	Urgency    string `json:"urgency"`
	AdviceText string `json:"adviceText"`
}

type errorBody struct {
	Error string `json:"error"`
}

type assignedFacility struct {
	FacilityID string
	Name       string
	District   string
}

type triageSession struct {
	SessionID              string
	District               string
	SymptomsText           string
	Urgency                string
	AdviceText             string
	CreatedAt              string
	ContactDoctorRequested bool
	ContactNumber          string
	FacilityID             string
	FacilityName           string
	Status                 string
}

type triageNotification struct {
	SessionID              string `json:"sessionId"`
	District               string `json:"district"`
	Urgency                string `json:"urgency"`
	ContactDoctorRequested bool   `json:"contactDoctorRequested"`
	FacilityID             string `json:"facilityId,omitempty"`
}

type triageGenerator interface {
	GenerateTriageJSON(ctx context.Context, req triageRequest) (string, error)
}

type sessionStore interface {
	SaveSession(ctx context.Context, session triageSession) error
}

type notificationPublisher interface {
	PublishTriageNotification(ctx context.Context, note triageNotification) error
}

type facilityAssigner interface {
	FindAvailableFacility(ctx context.Context, district string) (assignedFacility, bool, error)
}

var (
	aiClient   triageGenerator
	sessions   sessionStore
	notifier   notificationPublisher
	facilities facilityAssigner
	nowUTC     = func() time.Time { return time.Now().UTC() }
	newSession = newSessionID
)

func main() {
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" || apiKey == apiKeyPlaceholder {
		log.Fatal("GEMINI_API_KEY must be set to a real Gemini API key")
	}

	modelID := strings.TrimSpace(os.Getenv("GEMINI_MODEL_ID"))
	if modelID == "" {
		modelID = defaultGeminiModelID
	}

	tableName := strings.TrimSpace(os.Getenv("TRIAGE_SESSIONS_TABLE_NAME"))
	if tableName == "" {
		log.Fatal("TRIAGE_SESSIONS_TABLE_NAME must be set")
	}

	facilitiesTable := strings.TrimSpace(os.Getenv("FACILITIES_TABLE_NAME"))
	if facilitiesTable == "" {
		log.Fatal("FACILITIES_TABLE_NAME must be set")
	}

	topicARN := strings.TrimSpace(os.Getenv("TRIAGE_NOTIFICATIONS_TOPIC_ARN"))
	if topicARN == "" {
		log.Fatal("TRIAGE_NOTIFICATIONS_TOPIC_ARN must be set")
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("failed to create Gemini client: %v", err)
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	ddb := dynamodb.NewFromConfig(cfg)
	aiClient = &geminiGenerator{client: client, modelID: modelID}
	sessions = &dynamoSessionStore{client: ddb, tableName: tableName}
	facilities = &dynamoFacilityAssigner{client: ddb, tableName: facilitiesTable}
	notifier = &snsNotifier{client: sns.NewFromConfig(cfg), topicARN: topicARN}

	log.Printf("triage lambda starting: model=%q table=%q facilities=%q", modelID, tableName, facilitiesTable)
	lambda.Start(handler)
}

type geminiGenerator struct {
	client  *genai.Client
	modelID string
}

func (g *geminiGenerator) GenerateTriageJSON(ctx context.Context, req triageRequest) (string, error) {
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"urgency": {
				Type: genai.TypeString,
				Enum: []string{"self_care", "visit_soon", "emergency"},
			},
			"adviceText": {
				Type: genai.TypeString,
			},
		},
		Required: []string{"urgency", "adviceText"},
	}

	userMessage := "District: " + req.District + "\nSymptoms: " + req.SymptomsText

	genCfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
	}

	resp, err := g.client.Models.GenerateContent(ctx, g.modelID, genai.Text(userMessage), genCfg)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errInvalidModelOutput("empty generate content response")
	}

	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", errInvalidModelOutput("empty model text")
	}
	return text, nil
}

type dynamoSessionStore struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoSessionStore) SaveSession(ctx context.Context, session triageSession) error {
	item := map[string]types.AttributeValue{
		"sessionId":              &types.AttributeValueMemberS{Value: session.SessionID},
		"district":               &types.AttributeValueMemberS{Value: session.District},
		"symptomsText":           &types.AttributeValueMemberS{Value: session.SymptomsText},
		"urgency":                &types.AttributeValueMemberS{Value: session.Urgency},
		"adviceText":             &types.AttributeValueMemberS{Value: session.AdviceText},
		"createdAt":              &types.AttributeValueMemberS{Value: session.CreatedAt},
		"contactDoctorRequested": &types.AttributeValueMemberBOOL{Value: session.ContactDoctorRequested},
		"status":                 &types.AttributeValueMemberS{Value: session.Status},
	}
	if session.ContactNumber != "" {
		item["contactNumber"] = &types.AttributeValueMemberS{Value: session.ContactNumber}
	}
	if session.FacilityID != "" {
		item["facilityId"] = &types.AttributeValueMemberS{Value: session.FacilityID}
	}
	if session.FacilityName != "" {
		item["facilityName"] = &types.AttributeValueMemberS{Value: session.FacilityName}
	}
	_, err := d.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(d.tableName),
		Item:      item,
	})
	return err
}

type dynamoFacilityAssigner struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoFacilityAssigner) FindAvailableFacility(ctx context.Context, district string) (assignedFacility, bool, error) {
	var startKey map[string]types.AttributeValue
	candidates := make([]assignedFacility, 0)

	for {
		out, err := d.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(d.tableName),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return assignedFacility{}, false, err
		}
		for _, item := range out.Items {
			itemDistrict := attrString(item, "district")
			if !strings.EqualFold(strings.TrimSpace(itemDistrict), district) {
				continue
			}
			if !attrBool(item, "statusOpen") || !attrBool(item, "statusHasDoctor") {
				continue
			}
			candidates = append(candidates, assignedFacility{
				FacilityID: attrString(item, "facilityId"),
				Name:       attrString(item, "name"),
				District:   itemDistrict,
			})
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}

	if len(candidates) == 0 {
		return assignedFacility{}, false, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].FacilityID < candidates[j].FacilityID
	})
	return candidates[0], true, nil
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
	var req triageRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.SymptomsText = strings.TrimSpace(req.SymptomsText)
	req.District = strings.TrimSpace(req.District)
	req.ContactNumber = strings.TrimSpace(req.ContactNumber)
	if req.SymptomsText == "" || req.District == "" {
		log.Printf("validation failed: symptomsLen=%d districtSet=%t", len(req.SymptomsText), req.District != "")
		return jsonResponse(400, errorBody{Error: "symptomsText and district are required"})
	}

	if req.ContactNumber != "" {
		if errMsg := validateContactNumber(req.ContactNumber); errMsg != "" {
			log.Printf("validation failed: contactNumber invalid (len=%d)", len(req.ContactNumber))
			return jsonResponse(400, errorBody{Error: errMsg})
		}
	}

	// Do not log the contact number itself.
	log.Printf("triage request received: district=%q symptomsLen=%d contactProvided=%t",
		req.District, len(req.SymptomsText), req.ContactNumber != "")

	result, err := aiClient.GenerateTriageJSON(ctx, req)
	if err != nil {
		log.Printf("gemini generate failed: %v", err)
		return jsonResponse(500, errorBody{Error: "triage service unavailable"})
	}

	parsed, err := parseTriageResponse(result)
	if err != nil {
		log.Printf("invalid model response: reason=%v", err)
		return jsonResponse(500, errorBody{Error: "invalid triage response from model"})
	}

	contactRequested := false
	if req.ContactDoctorRequested != nil && *req.ContactDoctorRequested && parsed.Urgency != "emergency" {
		contactRequested = true
	}

	facilityMsg := "No currently available open facility with a doctor in this district."
	facilityID := ""
	facilityName := ""
	facilityAvailable := false

	assigned, found, ferr := facilities.FindAvailableFacility(ctx, req.District)
	if ferr != nil {
		log.Printf("facility lookup failed (continuing without assignment): district=%q err=%v", req.District, ferr)
		facilityMsg = "Facility lookup temporarily unavailable; session saved without facility assignment."
	} else if found {
		facilityAvailable = true
		facilityID = assigned.FacilityID
		facilityName = assigned.Name
		facilityMsg = "Assigned to an open, doctor-staffed facility in your district."
	}

	session := triageSession{
		SessionID:              newSession(),
		District:               req.District,
		SymptomsText:           req.SymptomsText,
		Urgency:                parsed.Urgency,
		AdviceText:             parsed.AdviceText,
		CreatedAt:              nowUTC().Format(time.RFC3339),
		ContactDoctorRequested: contactRequested,
		ContactNumber:          req.ContactNumber,
		FacilityID:             facilityID,
		FacilityName:           facilityName,
		Status:                 pendingStatus,
	}

	if err := sessions.SaveSession(ctx, session); err != nil {
		log.Printf("triage session persist failed: sessionId=%q err=%v", session.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to save triage session"})
	}

	note := triageNotification{
		SessionID:              session.SessionID,
		District:               session.District,
		Urgency:                session.Urgency,
		ContactDoctorRequested: session.ContactDoctorRequested,
		FacilityID:             session.FacilityID,
	}
	if err := notifier.PublishTriageNotification(ctx, note); err != nil {
		log.Printf("triage notification publish failed: sessionId=%q err=%v", session.SessionID, err)
		return jsonResponse(500, errorBody{Error: "failed to publish triage notification"})
	}

	return jsonResponse(200, triageResponse{
		SessionID:              session.SessionID,
		Urgency:                parsed.Urgency,
		AdviceText:             parsed.AdviceText,
		ContactDoctorRequested: session.ContactDoctorRequested,
		FacilityID:             facilityID,
		FacilityName:           facilityName,
		FacilityAvailable:      facilityAvailable,
		FacilityMessage:        facilityMsg,
		District:               req.District,
	})
}

func validateContactNumber(raw string) string {
	if !phoneDigitsPattern.MatchString(raw) {
		return "contactNumber format is invalid"
	}
	digits := 0
	for _, r := range raw {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	if digits < 7 || digits > 15 {
		return "contactNumber must contain 7 to 15 digits"
	}
	return ""
}

func parseTriageResponse(raw string) (modelTriageResult, error) {
	cleaned := stripJSONFences(raw)

	var resp modelTriageResult
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return modelTriageResult{}, errInvalidModelOutput("JSON parse failed")
	}

	resp.Urgency = strings.TrimSpace(resp.Urgency)
	resp.AdviceText = strings.TrimSpace(resp.AdviceText)

	if !validUrgencies[resp.Urgency] {
		return modelTriageResult{}, errInvalidModelOutput("invalid urgency value")
	}
	if resp.AdviceText == "" {
		return modelTriageResult{}, errInvalidModelOutput("empty adviceText")
	}

	return resp, nil
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "json") {
		s = strings.TrimSpace(s[4:])
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
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

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(nowUTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}

type modelOutputError struct {
	msg string
}

func (e modelOutputError) Error() string { return e.msg }

func errInvalidModelOutput(msg string) error {
	return modelOutputError{msg: msg}
}
