package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

type mockFacilityLister struct {
	facilities []facilityRecord
	err        error
}

func (m *mockFacilityLister) ListFacilities(ctx context.Context) ([]facilityRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]facilityRecord, len(m.facilities))
	copy(out, m.facilities)
	return out, nil
}

func floatPtr(v float64) *float64 { return &v }

func TestHandlerValidation(t *testing.T) {
	store = &mockFacilityLister{}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerSuccessDeterministicByFacilityID(t *testing.T) {
	store = &mockFacilityLister{
		facilities: []facilityRecord{
			{
				FacilityID: "fac-b", Name: "Clinic B", District: "Puri",
				Lat: floatPtr(19.8), Long: floatPtr(85.9),
				StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: false,
				LastUpdatedAt: "2026-09-19T10:00:00Z",
			},
			{
				FacilityID: "fac-a", Name: "Clinic A", District: "Puri",
				Lat: floatPtr(19.7), Long: floatPtr(85.8),
				StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true,
				LastUpdatedAt: "2026-09-19T11:00:00Z",
			},
			{
				FacilityID: "fac-c", Name: "Other Dist", District: "Cuttack",
				StatusOpen: true, StatusHasDoctor: true,
			},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"district": "Puri"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got facilityLookupResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FacilityID != "fac-a" || got.Name != "Clinic A" || got.District != "Puri" {
		t.Fatalf("unexpected facility: %+v", got)
	}
	if !got.StatusOpen || !got.StatusHasDoctor || !got.StatusHasMedicine {
		t.Fatalf("unexpected status flags: %+v", got)
	}
	if got.Lat == nil || *got.Lat != 19.7 || got.Long == nil || *got.Long != 85.8 {
		t.Fatalf("unexpected coords: %+v", got)
	}
	if got.SelectionMethod != "district_deterministic" {
		t.Fatalf("selectionMethod=%q", got.SelectionMethod)
	}
	if got.DistanceKm != nil {
		t.Fatalf("distanceKm should be null without citizen GPS, got %v", *got.DistanceKm)
	}
}

func TestHandlerDistrictCaseInsensitive(t *testing.T) {
	store = &mockFacilityLister{
		facilities: []facilityRecord{
			{
				FacilityID: "fac-1", Name: "PHC", District: "Khordha",
				StatusOpen: true, StatusHasDoctor: true,
			},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"district": "khordha"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerSkipsClosedOrUnstaffed(t *testing.T) {
	store = &mockFacilityLister{
		facilities: []facilityRecord{
			{FacilityID: "closed", District: "Puri", StatusOpen: false, StatusHasDoctor: true},
			{FacilityID: "no-doc", District: "Puri", StatusOpen: true, StatusHasDoctor: false},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"district": "Puri"},
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
	if body.Error != "no available facility found" {
		t.Fatalf("unexpected error: %+v", body)
	}
}

func TestHandlerBodyDistrictFallback(t *testing.T) {
	store = &mockFacilityLister{
		facilities: []facilityRecord{
			{FacilityID: "fac-1", Name: "PHC", District: "Cuttack", StatusOpen: true, StatusHasDoctor: true},
		},
	}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"district":"Cuttack"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerScanFailure(t *testing.T) {
	store = &mockFacilityLister{err: errors.New("scan failed")}

	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		QueryStringParameters: map[string]string{"district": "Puri"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d want=500 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHaversineKmKnownDistance(t *testing.T) {
	// Rough check: ~111 km per degree latitude near equator.
	d := haversineKm(0, 0, 1, 0)
	if d < 110 || d > 112 {
		t.Fatalf("haversineKm=%.2f want ~111", d)
	}
}
