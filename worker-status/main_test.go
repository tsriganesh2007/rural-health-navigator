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

func TestHandlerValidation(t *testing.T) {
	store = &mockStore{}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "invalid json", body: "{", wantStatus: 400},
		{name: "missing facilityId", body: `{"statusOpen":true,"statusHasDoctor":false,"statusHasMedicine":true,"lastUpdatedBy":"worker-1"}`, wantStatus: 400},
		{name: "missing lastUpdatedBy", body: `{"facilityId":"f1","statusOpen":true,"statusHasDoctor":false,"statusHasMedicine":true,"lastUpdatedBy":""}`, wantStatus: 400},
		{name: "missing statusOpen", body: `{"facilityId":"f1","statusHasDoctor":false,"statusHasMedicine":true,"lastUpdatedBy":"worker-1"}`, wantStatus: 400},
		{name: "missing statusHasDoctor", body: `{"facilityId":"f1","statusOpen":true,"statusHasMedicine":true,"lastUpdatedBy":"worker-1"}`, wantStatus: 400},
		{name: "missing statusHasMedicine", body: `{"facilityId":"f1","statusOpen":true,"statusHasDoctor":false,"lastUpdatedBy":"worker-1"}`, wantStatus: 400},
		{name: "valid false statuses", body: `{"facilityId":"f1","statusOpen":false,"statusHasDoctor":false,"statusHasMedicine":false,"lastUpdatedBy":"worker-1"}`, wantStatus: 200},
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
	mock := &mockStore{}
	store = mock
	fixed := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	nowUTC = func() time.Time { return fixed }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"facilityId":"fac-42","statusOpen":true,"statusHasDoctor":false,"statusHasMedicine":true,"lastUpdatedBy":"asha-7"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got workerStatusResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Message != "status updated" || got.FacilityID != "fac-42" || got.LastUpdatedAt != "2026-09-19T12:00:00Z" {
		t.Fatalf("unexpected body: %+v", got)
	}
	if mock.lastFacilityID != "fac-42" || !mock.lastOpen || mock.lastDoctor || !mock.lastMedicine || mock.lastBy != "asha-7" || mock.lastAt != "2026-09-19T12:00:00Z" {
		t.Fatalf("unexpected store args: %+v", mock)
	}
}

func TestHandlerFacilityNotFound(t *testing.T) {
	store = &mockStore{err: errFacilityNotFound}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"facilityId":"missing","statusOpen":true,"statusHasDoctor":true,"statusHasMedicine":true,"lastUpdatedBy":"worker-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d want=404 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerStoreFailure(t *testing.T) {
	store = &mockStore{err: errors.New("dynamo unavailable")}
	nowUTC = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"facilityId":"f1","statusOpen":true,"statusHasDoctor":true,"statusHasMedicine":true,"lastUpdatedBy":"worker-1"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
}
