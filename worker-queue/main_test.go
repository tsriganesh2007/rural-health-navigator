package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type mockLister struct {
	sessions []queueSession
	err      error
}

func (m *mockLister) ListSessions(ctx context.Context) ([]queueSession, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]queueSession, len(m.sessions))
	copy(out, m.sessions)
	return out, nil
}

func TestHandlerRequiresWorkerID(t *testing.T) {
	store = &mockLister{}
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400", resp.StatusCode)
	}
}

func TestHandlerUnknownWorker(t *testing.T) {
	store = &mockLister{}
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"workerId": "nope"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d want=404", resp.StatusCode)
	}
}

func TestHandlerFiltersToOwnFacilityAndOrdersPendingFirst(t *testing.T) {
	store = &mockLister{
		sessions: []queueSession{
			{SessionID: "other", District: "Khammam", FacilityID: "KHAMMAM-001", Status: pendingStatus, CreatedAt: "2026-09-20T12:00:00Z"},
			{SessionID: "ack-new", District: "Warangal", FacilityID: "WARANGAL-001", Status: acknowledgedStatus, CreatedAt: "2026-09-20T11:00:00Z", AcknowledgedBy: "worker-warangal-1", AcknowledgedAt: "2026-09-20T11:05:00Z", ContactNumber: "9999999999"},
			{SessionID: "pend-old", District: "Warangal", FacilityID: "WARANGAL-001", Status: pendingStatus, CreatedAt: "2026-09-19T10:00:00Z"},
			{SessionID: "pend-new", District: "Warangal", FacilityID: "WARANGAL-001", Status: pendingStatus, CreatedAt: "2026-09-20T10:00:00Z", ContactDoctorRequested: true, ContactNumber: "9876543210"},
			{SessionID: "ack-old", District: "Warangal", FacilityID: "WARANGAL-001", Status: acknowledgedStatus, CreatedAt: "2026-09-18T10:00:00Z"},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"workerId": "worker-warangal-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got []queueSession
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len=%d want=4 (other district excluded)", len(got))
	}
	if got[0].SessionID != "pend-new" || got[1].SessionID != "pend-old" || got[2].SessionID != "ack-new" || got[3].SessionID != "ack-old" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[0].ContactNumber != "9876543210" || !got[0].ContactDoctorRequested {
		t.Fatalf("pending contact fields missing: %+v", got[0])
	}
	if got[2].AcknowledgedBy != "worker-warangal-1" || got[2].AcknowledgedAt == "" {
		t.Fatalf("ack metadata missing: %+v", got[2])
	}
}

func TestHandlerLegacySessionMatchesDistrict(t *testing.T) {
	store = &mockLister{
		sessions: []queueSession{
			{SessionID: "legacy", District: "Warangal", Status: "", CreatedAt: "2026-09-19T12:00:00Z"},
			{SessionID: "other", District: "Nalgonda", Status: pendingStatus, CreatedAt: "2026-09-19T13:00:00Z"},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"workerId": "worker-warangal-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []queueSession
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "legacy" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got[0].Status != pendingStatus {
		t.Fatalf("legacy status should default to pending, got %q", got[0].Status)
	}
}

func TestHandlerEmptyQueue(t *testing.T) {
	store = &mockLister{sessions: nil}
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"workerId": "worker-warangal-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Body != "[]" {
		t.Fatalf("body=%q", resp.Body)
	}
}

func TestHandlerDynamoFailure(t *testing.T) {
	store = &mockLister{err: errors.New("scan failed")}
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"workerId": "worker-warangal-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHandlerContactNumberInResponse(t *testing.T) {
	store = &mockLister{
		sessions: []queueSession{
			{SessionID: "s1", District: "Warangal", FacilityID: "WARANGAL-001", Status: pendingStatus, CreatedAt: "2026-09-19T12:00:00Z", ContactNumber: "9123456789"},
		},
	}
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"workerId": "worker-warangal-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Body, `"contactNumber":"9123456789"`) {
		t.Fatalf("missing contactNumber: %s", resp.Body)
	}
}
