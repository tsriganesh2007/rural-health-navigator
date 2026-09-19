package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestHandlerValidCredentials(t *testing.T) {
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"username":"warangal.worker","password":"demo123"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	var got loginResponse
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.WorkerID != "worker-warangal-1" || got.District != "Warangal" || got.FacilityID != "WARANGAL-001" {
		t.Fatalf("unexpected worker mapping: %+v", got)
	}
	if got.FacilityName == "" || !got.DemoAuth {
		t.Fatalf("expected facility name and demoAuth flag: %+v", got)
	}
	if got.Username != "warangal.worker" {
		t.Fatalf("username=%q", got.Username)
	}
}

func TestHandlerInvalidCredentials(t *testing.T) {
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"username":"warangal.worker","password":"wrong"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d want=401 body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandlerValidation(t *testing.T) {
	resp, err := handler(context.Background(), events.APIGatewayProxyRequest{Body: `{`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400", resp.StatusCode)
	}

	resp, err = handler(context.Background(), events.APIGatewayProxyRequest{
		Body: `{"username":"","password":"demo123"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want=400", resp.StatusCode)
	}
}

func TestHandlerReturnsCorrectDistrictFacilityForEachDemoWorker(t *testing.T) {
	for _, w := range DemoWorkers {
		body, _ := json.Marshal(loginRequest{Username: w.Username, Password: w.Password})
		resp, err := handler(context.Background(), events.APIGatewayProxyRequest{Body: string(body)})
		if err != nil {
			t.Fatalf("%s: %v", w.Username, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s status=%d body=%s", w.Username, resp.StatusCode, resp.Body)
		}
		var got loginResponse
		if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
			t.Fatalf("%s unmarshal: %v", w.Username, err)
		}
		if got.District != w.District || got.FacilityID != w.FacilityID || got.WorkerID != w.WorkerID {
			t.Fatalf("%s mapping mismatch: %+v", w.Username, got)
		}
	}
}
