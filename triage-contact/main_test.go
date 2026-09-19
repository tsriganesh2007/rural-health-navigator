package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type mockContactStore struct {
	snap      sessionSnapshot
	getErr    error
	markErr   error
	getCalls  int
	markCalls int
	lastMark  string
}

func (m *mockContactStore) GetSession(ctx context.Context, sessionID string) (sessionSnapshot, error) {
	m.getCalls++
	if m.getErr != nil {
		return sessionSnapshot{}, m.getErr
	}
	return m.snap, nil
}

func (m *mockContactStore) MarkContactRequested(ctx context.Context, sessionID string) error {
	m.markCalls++
	m.lastMark = sessionID
	return m.markErr
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

func setupMocks(urgency string) (*mockContactStore, *mockNotifier) {
	store := &mockContactStore{
		snap: sessionSnapshot{
			SessionID: "sess-1",
			District:  "Puri",
			Urgency:   urgency,
		},
	}
	pub := &mockNotifier{}
	sessions = store
	notifier = pub
	return store, pub
}

func TestHandlerValidation(t *testing.T) {
	setupMocks("self_care")

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "invalid json", body: "{", wantStatus: 400},
		{name: "missing sessionId", body: `{"sessionId":""}`, wantStatus: 400},
		{name: "whitespace sessionId", body: `{"sessionId":"  "}`, wantStatus: 400},
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

func TestHandlerLowUrgencyDoctorRequest(t *testing.T) {
	store, pub := setupMocks("self_care")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got contactResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.ContactDoctorRequested || got.Urgency != "self_care" || got.SessionID != "sess-1" {
		t.Fatalf("unexpected body: %+v", got)
	}
	if store.markCalls != 1 || store.lastMark != "sess-1" {
		t.Fatalf("unexpected mark: calls=%d last=%q", store.markCalls, store.lastMark)
	}
	if pub.calls != 1 {
		t.Fatalf("notifier calls=%d want=1", pub.calls)
	}
	if !pub.last.ContactDoctorRequested || pub.last.Urgency != "self_care" {
		t.Fatalf("unexpected SNS note: %+v", pub.last)
	}
	if strings.Contains(pub.raw, "symptomsText") {
		t.Fatalf("SNS must not include symptoms: %s", pub.raw)
	}
	if !strings.Contains(pub.raw, `"contactDoctorRequested":true`) {
		t.Fatalf("SNS missing contactDoctorRequested: %s", pub.raw)
	}
}

func TestHandlerVisitSoonDoctorRequest(t *testing.T) {
	_, pub := setupMocks("visit_soon")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if pub.last.Urgency != "visit_soon" || !pub.last.ContactDoctorRequested {
		t.Fatalf("unexpected SNS: %+v", pub.last)
	}
}

func TestHandlerEmergencyRejected(t *testing.T) {
	store, pub := setupMocks("emergency")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400 body=%s", resp.StatusCode, resp.Body)
	}
	if store.markCalls != 0 || pub.calls != 0 {
		t.Fatalf("emergency must not mark/publish; mark=%d pub=%d", store.markCalls, pub.calls)
	}
}

func TestHandlerSessionNotFound(t *testing.T) {
	store, pub := setupMocks("self_care")
	store.getErr = errSessionNotFound

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"missing"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d want=404 body=%s", resp.StatusCode, resp.Body)
	}
	if pub.calls != 0 {
		t.Fatalf("notifier calls=%d want=0", pub.calls)
	}
}

func TestHandlerDoesNotOverwriteUrgency(t *testing.T) {
	_, pub := setupMocks("visit_soon")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got contactResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Urgency != "visit_soon" {
		t.Fatalf("urgency overwritten: %q", got.Urgency)
	}
	if pub.last.Urgency != "visit_soon" {
		t.Fatalf("SNS urgency overwritten: %q", pub.last.Urgency)
	}
}

func TestHandlerSNSFailure(t *testing.T) {
	store, pub := setupMocks("self_care")
	pub.err = errors.New("sns down")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
	if store.markCalls != 1 {
		t.Fatalf("mark should still happen before SNS; calls=%d", store.markCalls)
	}
}
