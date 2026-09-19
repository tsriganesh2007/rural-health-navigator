package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

type mockAckStore struct {
	snap        sessionSnapshot
	getErr      error
	ackErr      error
	ackCalls    int
	lastBy      string
	lastAt      string
	lastSession string
}

func (m *mockAckStore) GetSession(ctx context.Context, sessionID string) (sessionSnapshot, error) {
	if m.getErr != nil {
		return sessionSnapshot{}, m.getErr
	}
	return m.snap, nil
}

func (m *mockAckStore) Acknowledge(ctx context.Context, sessionID, workerID, acknowledgedAt string) error {
	m.ackCalls++
	m.lastSession = sessionID
	m.lastBy = workerID
	m.lastAt = acknowledgedAt
	return m.ackErr
}

func setupAckMocks() *mockAckStore {
	mock := &mockAckStore{
		snap: sessionSnapshot{
			SessionID:  "sess-1",
			District:   "Warangal",
			FacilityID: "WARANGAL-001",
			Status:     pendingStatus,
		},
	}
	store = mock
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }
	return mock
}

func TestHandlerAckPendingSuccess(t *testing.T) {
	mock := setupAckMocks()

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1","workerId":"worker-warangal-1"}`,
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
	if got.Status != acknowledgedStatus || got.AcknowledgedBy != "worker-warangal-1" || got.AcknowledgedAt != "2026-09-19T12:00:00Z" {
		t.Fatalf("unexpected body: %+v", got)
	}
	if mock.ackCalls != 1 || mock.lastBy != "worker-warangal-1" {
		t.Fatalf("unexpected ack store: %+v", mock)
	}
}

func TestHandlerSecondWorkerCannotClaim(t *testing.T) {
	mock := setupAckMocks()
	mock.snap.Status = acknowledgedStatus

	// Same facility worker cannot re-claim.
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1","workerId":"worker-warangal-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 409 {
		t.Fatalf("status=%d want=409 body=%s", resp.StatusCode, resp.Body)
	}
	if mock.ackCalls != 0 {
		t.Fatalf("should not ack again")
	}

	// Different-district worker is forbidden (wrong facility), not allowed to claim.
	resp, err = handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1","workerId":"worker-khammam-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("other-district worker status=%d want=403 body=%s", resp.StatusCode, resp.Body)
	}
	if mock.ackCalls != 0 {
		t.Fatalf("other worker must not ack")
	}
}

func TestHandlerConditionalRaceAlreadyAcknowledged(t *testing.T) {
	mock := setupAckMocks()
	mock.ackErr = errAlreadyAcknowledged

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1","workerId":"worker-warangal-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 409 {
		t.Fatalf("status=%d want=409", resp.StatusCode)
	}
}

func TestHandlerWrongFacilityForbidden(t *testing.T) {
	mock := setupAckMocks()
	mock.snap.FacilityID = "KHAMMAM-001"
	mock.snap.District = "Khammam"

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1","workerId":"worker-warangal-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("status=%d want=403 body=%s", resp.StatusCode, resp.Body)
	}
	if mock.ackCalls != 0 {
		t.Fatalf("must not ack other facility")
	}
}

func TestHandlerValidation(t *testing.T) {
	setupAckMocks()
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{Body: `{`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	resp, err = handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHandlerSessionNotFound(t *testing.T) {
	mock := setupAckMocks()
	mock.getErr = errSessionNotFound

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"missing","workerId":"worker-warangal-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHandlerStoreFailure(t *testing.T) {
	mock := setupAckMocks()
	mock.ackErr = errors.New("dynamo down")

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"sessionId":"sess-1","workerId":"worker-warangal-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
