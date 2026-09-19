package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

type mockSessionStore struct {
	err   error
	calls int
	last  triageSession
}

func (m *mockSessionStore) SaveSession(ctx context.Context, session triageSession) error {
	m.calls++
	m.last = session
	return m.err
}

type mockNotifier struct {
	err   error
	calls int
	last  triageNotification
	raw   string
}

func (m *mockNotifier) PublishTriageNotification(ctx context.Context, note triageNotification) error {
	m.calls++
	m.last = note
	b, err := json.Marshal(note)
	if err != nil {
		return err
	}
	m.raw = string(b)
	return m.err
}

func setupHappyPathMocks() (*mockSessionStore, *mockNotifier) {
	store := &mockSessionStore{}
	pub := &mockNotifier{}
	aiClient = &mockGemini{text: `{"urgency":"visit_soon","adviceText":"See a clinician soon."}`}
	sessions = store
	notifier = pub
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }
	newSession = func() string { return "sess-fixed-1" }
	return store, pub
}

func TestHandlerValidation(t *testing.T) {
	setupHappyPathMocks()

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
			setupHappyPathMocks()
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

func TestHandlerSuccessPersistsAndPublishes(t *testing.T) {
	store, pub := setupHappyPathMocks()
	symptoms := "persistent cough for three days"

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"` + symptoms + `","district":"Cuttack"}`,
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
	if got.SessionID != "sess-fixed-1" || got.Urgency != "visit_soon" || got.AdviceText != "See a clinician soon." {
		t.Fatalf("unexpected body: %+v", got)
	}
	if got.ContactDoctorRequested {
		t.Fatalf("expected contactDoctorRequested=false by default, got true")
	}

	if store.calls != 1 {
		t.Fatalf("store calls=%d want=1", store.calls)
	}
	if store.last.SessionID != "sess-fixed-1" || store.last.District != "Cuttack" || store.last.Urgency != "visit_soon" {
		t.Fatalf("unexpected persisted session: %+v", store.last)
	}
	if store.last.SymptomsText != symptoms {
		t.Fatalf("expected symptoms persisted, got %q", store.last.SymptomsText)
	}
	if store.last.ContactDoctorRequested {
		t.Fatalf("expected persisted contactDoctorRequested=false")
	}

	if pub.calls != 1 {
		t.Fatalf("notifier calls=%d want=1", pub.calls)
	}
	if pub.last.SessionID != "sess-fixed-1" || pub.last.District != "Cuttack" || pub.last.Urgency != "visit_soon" {
		t.Fatalf("unexpected notification: %+v", pub.last)
	}
	if pub.last.ContactDoctorRequested {
		t.Fatalf("expected SNS contactDoctorRequested=false")
	}
	if strings.Contains(pub.raw, symptoms) || strings.Contains(pub.raw, "symptomsText") {
		t.Fatalf("SNS payload must not include symptoms: %s", pub.raw)
	}
	if !strings.Contains(pub.raw, `"sessionId"`) || !strings.Contains(pub.raw, `"district"`) || !strings.Contains(pub.raw, `"urgency"`) {
		t.Fatalf("SNS payload missing required fields: %s", pub.raw)
	}
	if !strings.Contains(pub.raw, `"contactDoctorRequested":false`) {
		t.Fatalf("SNS payload missing contactDoctorRequested: %s", pub.raw)
	}
}

func TestHandlerLowUrgencyWithDoctorRequest(t *testing.T) {
	store, pub := setupHappyPathMocks()
	aiClient = &mockGemini{text: `{"urgency":"self_care","adviceText":"Rest and hydrate."}`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"mild cold","district":"Puri","contactDoctorRequested":true}`,
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
	if got.Urgency != "self_care" {
		t.Fatalf("urgency must stay self_care, got %q", got.Urgency)
	}
	if !got.ContactDoctorRequested {
		t.Fatalf("expected contactDoctorRequested=true")
	}
	if !store.last.ContactDoctorRequested || store.last.Urgency != "self_care" {
		t.Fatalf("unexpected persisted session: %+v", store.last)
	}
	if !pub.last.ContactDoctorRequested || pub.last.Urgency != "self_care" {
		t.Fatalf("unexpected SNS: %+v", pub.last)
	}
	if strings.Contains(pub.raw, "symptomsText") {
		t.Fatalf("SNS must not include symptoms: %s", pub.raw)
	}
}

func TestHandlerEmergencyIgnoresDoctorRequest(t *testing.T) {
	store, pub := setupHappyPathMocks()
	aiClient = &mockGemini{text: `{"urgency":"emergency","adviceText":"Seek emergency care immediately."}`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"severe chest pain","district":"Khordha","contactDoctorRequested":true}`,
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
	if got.Urgency != "emergency" {
		t.Fatalf("urgency=%q", got.Urgency)
	}
	if got.ContactDoctorRequested {
		t.Fatalf("emergency must not set contactDoctorRequested")
	}
	if store.last.ContactDoctorRequested || pub.last.ContactDoctorRequested {
		t.Fatalf("emergency must persist/publish contactDoctorRequested=false")
	}
}

func TestHandlerPersistFailureSkipsSNS(t *testing.T) {
	store, pub := setupHappyPathMocks()
	store.err = errors.New("dynamo unavailable")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"headache","district":"Puri"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want=1", store.calls)
	}
	if pub.calls != 0 {
		t.Fatalf("notifier calls=%d want=0", pub.calls)
	}
}

func TestHandlerSNSFailureDoesNotClaimSuccess(t *testing.T) {
	store, pub := setupHappyPathMocks()
	pub.err = errors.New("sns unavailable")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"dizziness","district":"Khordha"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want=1 (session remains persisted)", store.calls)
	}
	if pub.calls != 1 {
		t.Fatalf("notifier calls=%d want=1", pub.calls)
	}

	var body errorBody
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error != "failed to publish triage notification" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestHandlerGeminiFailure(t *testing.T) {
	setupHappyPathMocks()
	aiClient = &mockGemini{err: errors.New("gemini unavailable")}
	store := sessions.(*mockSessionStore)
	pub := notifier.(*mockNotifier)

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"chest pain","district":"Khordha"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
	if store.calls != 0 || pub.calls != 0 {
		t.Fatalf("expected no persist/publish on gemini failure; store=%d pub=%d", store.calls, pub.calls)
	}
}

func TestHandlerInvalidModelJSON(t *testing.T) {
	setupHappyPathMocks()
	aiClient = &mockGemini{text: `not json`}
	store := sessions.(*mockSessionStore)
	pub := notifier.(*mockNotifier)

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"headache","district":"Puri"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
	if store.calls != 0 || pub.calls != 0 {
		t.Fatalf("expected no persist/publish; store=%d pub=%d", store.calls, pub.calls)
	}
}

func TestHandlerInvalidUrgency(t *testing.T) {
	setupHappyPathMocks()
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
