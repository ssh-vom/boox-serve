package main

import "testing"

func TestConfigBaseURLWithExplicitURL(t *testing.T) {
	cfg := Config{BooxURL: "http://192.168.1.10:8085", BooxPort: 8085}

	baseURL, err := cfg.BaseURL()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if baseURL != "http://192.168.1.10:8085" {
		t.Fatalf("unexpected base URL: %s", baseURL)
	}
}

func TestConfigBaseURLFromIP(t *testing.T) {
	cfg := Config{BooxIP: "192.168.1.5", BooxPort: 8085}

	baseURL, err := cfg.BaseURL()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if baseURL != "http://192.168.1.5:8085" {
		t.Fatalf("unexpected base URL: %s", baseURL)
	}
}

func TestConfigBaseURLRequiresValue(t *testing.T) {
	cfg := Config{BooxPort: 8085}

	if _, err := cfg.BaseURL(); err == nil {
		t.Fatalf("expected error when no URL or IP is set")
	}
}
