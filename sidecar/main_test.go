package main

import (
	"strings"
	"testing"
)

func TestHandleRejectsMissingKey(t *testing.T) {
	response := handle(strings.NewReader(`{"action":"models","credentials":{"provider":"openai","baseUrl":"https://api.openai.com/v1","apiKey":""}}`))
	if response.Error != "请填写 API Key" {
		t.Fatalf("unexpected error: %s", response.Error)
	}
}
