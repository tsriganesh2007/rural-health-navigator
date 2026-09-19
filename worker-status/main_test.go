package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

type mockStore struct {
	err            error
	lastFacilityID string
	lastOpen       bool
	lastDoctor     bool
	lastMedicine   bool
	lastBy         string
	lastAt         string
}

func (m *mockStore) UpdateStatus(ctx context.Context, facilityID string, statusOpen, statusHasDoctor, statusHasMedicine bool, lastUpdatedBy, lastUpdatedAt string) error {
	m.lastFacilityID = facilityID
	m.lastOpen = statusOpen
	m.lastDoctor = statusHasDoctor
	m.lastMedicine = statusHasMedicine
	m.lastBy = lastUpdatedBy
	m.lastAt = lastUpdatedAt
	return m.err
}

func TestHandlerUpdatesOwnFacilityFromWorkerIdentity(t *testing.T) {
	mock := &mockStore{}
	store = mock
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"workerId":"worker-warangal-1","statusOpen":true,"statusHasDoctor":false,"statusHasMedicine":true}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if mock.lastFacilityID != "WARANGAL-001" || mock.lastBy != "worker-warangal-1" {
		t.Fatalf("unexpected store args: %+v", mock)
	}
	if mock.lastDoctor {
		t.Fatalf("expected doctor false")
	}

	var got workerStatusResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FacilityID != "WARANGAL-001" || got.District != "Warangal" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestHandlerCannotTargetAnotherFacilityViaBody(t *testing.T) {
	mock := &mockStore{}
	store = mock
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	// Even if a client tries to smuggle another facilityId, only workerId is used.
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"workerId":"worker-warangal-1","facilityId":"KHAMMAM-001","statusOpen":false,"statusHasDoctor":false,"statusHasMedicine":false}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if mock.lastFacilityID != "WARANGAL-001" {
		t.Fatalf("must resolve facility from worker identity, got %q", mock.lastFacilityID)
	}
}

func TestHandlerUnknownWorker(t *testing.T) {
	store = &mockStore{}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"workerId":"unknown","statusOpen":true,"statusHasDoctor":true,"statusHasMedicine":true}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d want=404", resp.StatusCode)
	}
}

func TestHandlerValidation(t *testing.T) {
	store = &mockStore{}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{Body: `{`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	resp, err = handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"workerId":"worker-warangal-1","statusOpen":true}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHandlerFacilityNotFound(t *testing.T) {
	store = &mockStore{err: errFacilityNotFound}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"workerId":"worker-warangal-1","statusOpen":true,"statusHasDoctor":true,"statusHasMedicine":true}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHandlerStoreFailure(t *testing.T) {
	store = &mockStore{err: errors.New("dynamo unavailable")}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"workerId":"worker-warangal-1","statusOpen":true,"statusHasDoctor":true,"statusHasMedicine":true}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
