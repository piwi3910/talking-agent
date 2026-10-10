package main

import "testing"

func TestValidateLLMKey(t *testing.T) {
	t.Setenv("TEST_LLM_KEY", "")
	if err := validateLLMKey("demo", "TEST_LLM_KEY"); err != nil {
		t.Fatalf("demo mode should not require a key: %v", err)
	}
	if err := validateLLMKey("openai-compatible", "TEST_LLM_KEY"); err == nil {
		t.Fatal("expected missing key error")
	}
	t.Setenv("TEST_LLM_KEY", "  \t")
	if err := validateLLMKey("openai-compatible", "TEST_LLM_KEY"); err == nil {
		t.Fatal("expected whitespace-only key error")
	}
	t.Setenv("TEST_LLM_KEY", "configured")
	if err := validateLLMKey("openai-compatible", "TEST_LLM_KEY"); err != nil {
		t.Fatalf("configured key rejected: %v", err)
	}
}
