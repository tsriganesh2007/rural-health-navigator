package main

import (
	"context"
	"encoding/json"
	"errors"
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

func TestHandlerSuccessSortedNewestFirst(t *testing.T) {
	store = &mockLister{
		sessions: []queueSession{
			{SessionID: "s1", District: "Puri", SymptomsText: "fever", Urgency: "self_care", AdviceText: "Rest", CreatedAt: "2026-09-18T10:00:00Z"},
			{SessionID: "s2", District: "Cuttack", SymptomsText: "cough", Urgency: "visit_soon", AdviceText: "Clinic", CreatedAt: "2026-09-19T12:00:00Z"},
			{SessionID: "s3", District: "Khordha", SymptomsText: "pain", Urgency: "emergency", AdviceText: "ER", CreatedAt: "2026-09-19T08:00:00Z"},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{})
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
	if len(got) != 3 {
		t.Fatalf("len=%d want=3", len(got))
	}
	if got[0].SessionID != "s2" || got[1].SessionID != "s3" || got[2].SessionID != "s1" {
		t.Fatalf("unexpected order: %+v", got)
	}

	first := got[0]
	if first.District != "Cuttack" || first.SymptomsText != "cough" || first.Urgency != "visit_soon" ||
		first.AdviceText != "Clinic" || first.CreatedAt != "2026-09-19T12:00:00Z" {
		t.Fatalf("unexpected fields: %+v", first)
	}
}

func TestHandlerEmptyQueue(t *testing.T) {
	store = &mockLister{sessions: nil}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if resp.Body != "[]" {
		t.Fatalf("body=%q want=[]", resp.Body)
	}
}

func TestHandlerDynamoFailure(t *testing.T) {
	store = &mockLister{err: errors.New("scan failed")}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}

	var body errorBody
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error != "failed to load worker queue" {
		t.Fatalf("unexpected error: %+v", body)
	}
}

func TestHandlerRespectsQueueLimit(t *testing.T) {
	sessions := make([]queueSession, 0, queueLimit+5)
	for i := 0; i < queueLimit+5; i++ {
		sessions = append(sessions, queueSession{
			SessionID: "s-" + string(rune('0'+i)),
			CreatedAt: "2026-09-19T12:00:00Z",
		})
	}
	// Make one clearly newest so order is deterministic at the front.
	sessions[0].SessionID = "newest"
	sessions[0].CreatedAt = "2026-09-20T00:00:00Z"
	store = &mockLister{sessions: sessions}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{})
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
	if len(got) != queueLimit {
		t.Fatalf("len=%d want=%d", len(got), queueLimit)
	}
	if got[0].SessionID != "newest" {
		t.Fatalf("expected newest first, got %+v", got[0])
	}
}
