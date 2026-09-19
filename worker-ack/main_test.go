package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type mockStore struct {
	err            error
	calls          int
	lastSessionID  string
	updateStatuses []string
}

func (m *mockStore) Acknowledge(ctx context.Context, sessionID string) error {
	m.calls++
	m.lastSessionID = sessionID
	m.updateStatuses = append(m.updateStatuses, acknowledgedStatus)
	return m.err
}

func TestHandlerSuccess(t *testing.T) {
	mock := &mockStore{}
	store = mock

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-42"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got ackResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Message != "session acknowledged" || got.SessionID != "sess-42" || got.Status != "acknowledged" {
		t.Fatalf("unexpected body: %+v", got)
	}
	if mock.calls != 1 || mock.lastSessionID != "sess-42" {
		t.Fatalf("unexpected store call: %+v", mock)
	}
	if len(mock.updateStatuses) != 1 || mock.updateStatuses[0] != "acknowledged" {
		t.Fatalf("expected only status=acknowledged update, got %+v", mock.updateStatuses)
	}
}

func TestHandlerMissingSessionID(t *testing.T) {
	store = &mockStore{}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerEmptySessionID(t *testing.T) {
	store = &mockStore{}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"   "}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerInvalidJSON(t *testing.T) {
	store = &mockStore{}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerSessionNotFound(t *testing.T) {
	store = &mockStore{err: errSessionNotFound}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"missing"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d want=404 body=%s", resp.StatusCode, resp.Body)
	}

	var body errorBody
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error != "session not found" {
		t.Fatalf("unexpected error: %+v", body)
	}
}

func TestHandlerDynamoFailure(t *testing.T) {
	store = &mockStore{err: errors.New("dynamo unavailable")}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
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
	if body.Error != "failed to acknowledge session" {
		t.Fatalf("unexpected error: %+v", body)
	}
}
