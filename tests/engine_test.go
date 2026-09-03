package main

import (
	"encoding/json"
	"testing"
)

// InboundPayload schema match tracker from engine.go
type InboundPayload struct {
	TargetURL      string            `json:"target_url"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	RequestHeaders map[string]string `json:"request_headers"`
}

func TestJSONPayloadUnmarshalling(t *testing.T) {
	mockPayload := []byte(`{"target_url":"https://example.com","timeout_seconds":10,"request_headers":{"X-Test-Header":"Passed"}}`)
	
	var payload InboundPayload
	if err := json.Unmarshal(mockPayload, &payload); err != nil {
		t.Fatalf("CRITICAL: Failed to decode standard JSON contract payload: %v", err)
	}
	
	if payload.TargetURL != "https://example.com" {
		t.Errorf("FAIL: Unexpected dynamic URL routing assignment. Got: %s", payload.TargetURL)
	}
	
	if payload.RequestHeaders["X-Test-Header"] != "Passed" {
		t.Errorf("FAIL: Request header dynamic mapping failed.")
	}
}
