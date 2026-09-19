package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"google.golang.org/genai"
)

const (
	defaultGeminiModelID = "gemini-3.5-flash-lite"
	apiKeyPlaceholder    = "REPLACE_WITH_GEMINI_API_KEY"
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

type triageRequest struct {
	SymptomsText string `json:"symptomsText"`
	District     string `json:"district"`
}

type triageResponse struct {
	Urgency    string `json:"urgency"`
	AdviceText string `json:"adviceText"`
}

type errorBody struct {
	Error string `json:"error"`
}

// triageGenerator produces model JSON for a triage request (mocked in tests).
type triageGenerator interface {
	GenerateTriageJSON(ctx context.Context, req triageRequest) (string, error)
}

var aiClient triageGenerator

func main() {
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" || apiKey == apiKeyPlaceholder {
		log.Fatal("GEMINI_API_KEY must be set to a real Gemini API key")
	}

	modelID := strings.TrimSpace(os.Getenv("GEMINI_MODEL_ID"))
	if modelID == "" {
		modelID = defaultGeminiModelID
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("failed to create Gemini client: %v", err)
	}

	aiClient = &geminiGenerator{client: client, modelID: modelID}
	log.Printf("triage lambda starting: model=%q", modelID)
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

	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
	}

	resp, err := g.client.Models.GenerateContent(ctx, g.modelID, genai.Text(userMessage), config)
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

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	var req triageRequest
	if err := json.Unmarshal([]byte(request.Body), &req); err != nil {
		log.Printf("invalid request body: %v", err)
		return jsonResponse(400, errorBody{Error: "invalid JSON body"})
	}

	req.SymptomsText = strings.TrimSpace(req.SymptomsText)
	req.District = strings.TrimSpace(req.District)
	if req.SymptomsText == "" || req.District == "" {
		log.Printf("validation failed: symptomsLen=%d districtSet=%t", len(req.SymptomsText), req.District != "")
		return jsonResponse(400, errorBody{Error: "symptomsText and district are required"})
	}

	log.Printf("triage request received: district=%q symptomsLen=%d", req.District, len(req.SymptomsText))

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

	return jsonResponse(200, parsed)
}

func parseTriageResponse(raw string) (triageResponse, error) {
	cleaned := stripJSONFences(raw)

	var resp triageResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return triageResponse{}, errInvalidModelOutput("JSON parse failed")
	}

	resp.Urgency = strings.TrimSpace(resp.Urgency)
	resp.AdviceText = strings.TrimSpace(resp.AdviceText)

	if !validUrgencies[resp.Urgency] {
		return triageResponse{}, errInvalidModelOutput("invalid urgency value")
	}
	if resp.AdviceText == "" {
		return triageResponse{}, errInvalidModelOutput("empty adviceText")
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
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"error":"internal error"}`,
		}, nil
	}
	return events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(b),
	}, nil
}

type modelOutputError struct {
	msg string
}

func (e modelOutputError) Error() string { return e.msg }

func errInvalidModelOutput(msg string) error {
	return modelOutputError{msg: msg}
}
