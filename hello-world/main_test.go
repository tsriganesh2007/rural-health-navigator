package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type mockGemini struct {
	text string
	err  error
}

func (m *mockGemini) GenerateTriageJSON(ctx context.Context, req triageRequest) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.text, nil
}

func TestHandlerValidation(t *testing.T) {
	aiClient = &mockGemini{text: `{"urgency":"self_care","adviceText":"Rest and hydrate."}`}

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "invalid json", body: "{", wantStatus: 400},
		{name: "missing symptoms", body: `{"symptomsText":"","district":"Puri"}`, wantStatus: 400},
		{name: "missing district", body: `{"symptomsText":"fever","district":""}`, wantStatus: 400},
		{name: "whitespace only", body: `{"symptomsText":"  ","district":"  "}`, wantStatus: 400},
		{name: "valid", body: `{"symptomsText":"mild fever","district":"Puri"}`, wantStatus: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handler(context.Background(), events.APIGatewayProxyRequest{Body: tt.body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, tt.wantStatus, resp.Body)
			}
		})
	}
}

func TestHandlerSuccess(t *testing.T) {
	aiClient = &mockGemini{text: `{"urgency":"visit_soon","adviceText":"See a clinician soon."}`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"persistent cough","district":"Cuttack"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got triageResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Urgency != "visit_soon" || got.AdviceText != "See a clinician soon." {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestHandlerGeminiFailure(t *testing.T) {
	aiClient = &mockGemini{err: errors.New("gemini unavailable")}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"chest pain","district":"Khordha"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerInvalidModelJSON(t *testing.T) {
	aiClient = &mockGemini{text: `not json`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"headache","district":"Puri"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerInvalidUrgency(t *testing.T) {
	aiClient = &mockGemini{text: `{"urgency":"critical","adviceText":"Go now"}`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"dizziness","district":"Puri"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestParseTriageResponseFencedJSON(t *testing.T) {
	raw := "```json\n{\"urgency\":\"emergency\",\"adviceText\":\"Seek emergency care now.\"}\n```"
	got, err := parseTriageResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Urgency != "emergency" {
		t.Fatalf("urgency=%q", got.Urgency)
	}
}
