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

type mockFacilityAssigner struct {
	facility assignedFacility
	found    bool
	err      error
}

func (m *mockFacilityAssigner) FindAvailableFacility(ctx context.Context, district string) (assignedFacility, bool, error) {
	if m.err != nil {
		return assignedFacility{}, false, m.err
	}
	return m.facility, m.found, nil
}

func setupHappyPathMocks() (*mockSessionStore, *mockNotifier, *mockFacilityAssigner) {
	store := &mockSessionStore{}
	pub := &mockNotifier{}
	fac := &mockFacilityAssigner{
		found: true,
		facility: assignedFacility{
			FacilityID: "WARANGAL-001",
			Name:       "Warangal Demo Community Health Centre",
			District:   "Warangal",
		},
	}
	aiClient = &mockGemini{text: `{"urgency":"visit_soon","adviceText":"See a clinician soon."}`}
	sessions = store
	notifier = pub
	facilities = fac
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }
	newSession = func() string { return "sess-fixed-1" }
	return store, pub, fac
}

func TestHandlerValidation(t *testing.T) {
	setupHappyPathMocks()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "invalid json", body: "{", wantStatus: 400},
		{name: "missing symptoms", body: `{"symptomsText":"","district":"Warangal"}`, wantStatus: 400},
		{name: "missing district", body: `{"symptomsText":"fever","district":""}`, wantStatus: 400},
		{name: "bad phone", body: `{"symptomsText":"fever","district":"Warangal","contactNumber":"abc"}`, wantStatus: 400},
		{name: "valid", body: `{"symptomsText":"mild fever","district":"Warangal"}`, wantStatus: 200},
		{name: "valid with phone", body: `{"symptomsText":"mild fever","district":"Warangal","contactNumber":"+91 98765 43210"}`, wantStatus: 200},
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

func TestHandlerAssignsFacilityAndPreservesDistrict(t *testing.T) {
	store, pub, _ := setupHappyPathMocks()

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"cough","district":"Warangal","contactNumber":"9876543210"}`,
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
	if got.District != "Warangal" || !got.FacilityAvailable || got.FacilityID != "WARANGAL-001" {
		t.Fatalf("unexpected facility assignment: %+v", got)
	}
	if strings.Contains(resp.Body, "9876543210") || strings.Contains(resp.Body, "contactNumber") {
		t.Fatalf("citizen response must not echo contact number: %s", resp.Body)
	}
	if store.last.ContactNumber != "9876543210" || store.last.FacilityID != "WARANGAL-001" || store.last.Status != pendingStatus {
		t.Fatalf("unexpected persisted session: %+v", store.last)
	}
	if store.last.District != "Warangal" {
		t.Fatalf("district not preserved: %q", store.last.District)
	}
	if strings.Contains(pub.raw, "9876543210") || strings.Contains(pub.raw, "contactNumber") || strings.Contains(pub.raw, "symptomsText") {
		t.Fatalf("SNS must omit phone and symptoms: %s", pub.raw)
	}
}

func TestHandlerNoFacilityDoesNotFailTriage(t *testing.T) {
	store, _, fac := setupHappyPathMocks()
	fac.found = false

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"headache","district":"Warangal"}`,
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
	if got.FacilityAvailable || got.FacilityID != "" {
		t.Fatalf("expected no facility: %+v", got)
	}
	if !strings.Contains(got.FacilityMessage, "No currently available") {
		t.Fatalf("facility message=%q", got.FacilityMessage)
	}
	if store.calls != 1 || store.last.FacilityID != "" || store.last.Status != pendingStatus {
		t.Fatalf("session should still persist: %+v", store.last)
	}
}

func TestHandlerFacilityLookupErrorStillSaves(t *testing.T) {
	store, _, fac := setupHappyPathMocks()
	fac.err = errors.New("scan failed")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"rash","district":"Khammam"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d", store.calls)
	}
}

func TestHandlerContactNumberOptional(t *testing.T) {
	store, _, _ := setupHappyPathMocks()
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"mild fever","district":"Nalgonda"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if store.last.ContactNumber != "" {
		t.Fatalf("expected empty contactNumber, got %q", store.last.ContactNumber)
	}
}

func TestHandlerLowUrgencyWithDoctorRequest(t *testing.T) {
	store, pub, _ := setupHappyPathMocks()
	aiClient = &mockGemini{text: `{"urgency":"self_care","adviceText":"Rest and hydrate."}`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"mild cold","district":"Warangal","contactDoctorRequested":true}`,
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
	if got.Urgency != "self_care" || !got.ContactDoctorRequested {
		t.Fatalf("unexpected body: %+v", got)
	}
	if store.last.Urgency != "self_care" || !store.last.ContactDoctorRequested {
		t.Fatalf("unexpected session: %+v", store.last)
	}
	if pub.last.Urgency != "self_care" || !pub.last.ContactDoctorRequested {
		t.Fatalf("unexpected SNS: %+v", pub.last)
	}
}

func TestHandlerEmergencyIgnoresDoctorRequest(t *testing.T) {
	store, pub, _ := setupHappyPathMocks()
	aiClient = &mockGemini{text: `{"urgency":"emergency","adviceText":"Seek emergency care immediately."}`}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"severe chest pain","district":"Warangal","contactDoctorRequested":true}`,
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
	if got.Urgency != "emergency" || got.ContactDoctorRequested {
		t.Fatalf("unexpected body: %+v", got)
	}
	if store.last.ContactDoctorRequested || pub.last.ContactDoctorRequested {
		t.Fatalf("emergency must keep contactDoctorRequested=false")
	}
}

func TestHandlerPersistFailureSkipsSNS(t *testing.T) {
	store, pub, _ := setupHappyPathMocks()
	store.err = errors.New("dynamo unavailable")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"headache","district":"Warangal"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 || store.calls != 1 || pub.calls != 0 {
		t.Fatalf("status=%d store=%d pub=%d", resp.StatusCode, store.calls, pub.calls)
	}
}

func TestHandlerSNSFailureDoesNotClaimSuccess(t *testing.T) {
	store, pub, _ := setupHappyPathMocks()
	pub.err = errors.New("sns unavailable")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"dizziness","district":"Warangal"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 || store.calls != 1 || pub.calls != 1 {
		t.Fatalf("status=%d store=%d pub=%d", resp.StatusCode, store.calls, pub.calls)
	}
}

func TestHandlerGeminiFailure(t *testing.T) {
	setupHappyPathMocks()
	aiClient = &mockGemini{err: errors.New("gemini unavailable")}
	store := sessions.(*mockSessionStore)
	pub := notifier.(*mockNotifier)

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"symptomsText":"chest pain","district":"Warangal"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 || store.calls != 0 || pub.calls != 0 {
		t.Fatalf("status=%d store=%d pub=%d", resp.StatusCode, store.calls, pub.calls)
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

func TestValidateContactNumber(t *testing.T) {
	if msg := validateContactNumber("9876543210"); msg != "" {
		t.Fatalf("valid phone rejected: %s", msg)
	}
	if msg := validateContactNumber("12"); msg == "" {
		t.Fatal("short phone should fail")
	}
}
