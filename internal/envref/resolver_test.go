package envref

import "testing"

func TestParseDotEnv(t *testing.T) {
	raw := `
# comment line
export PORT=3000
OPENAI_API_KEY="sk-test-123"
APP_NAME='Pleum Router'
`
	parsed, err := ParseDotEnv(raw)
	if err != nil {
		t.Fatalf("ParseDotEnv: %v", err)
	}
	if parsed["PORT"] != "3000" {
		t.Fatalf("PORT got %q", parsed["PORT"])
	}
	if parsed["OPENAI_API_KEY"] != "sk-test-123" {
		t.Fatalf("OPENAI_API_KEY got %q", parsed["OPENAI_API_KEY"])
	}
	if parsed["APP_NAME"] != "Pleum Router" {
		t.Fatalf("APP_NAME got %q", parsed["APP_NAME"])
	}
}

func TestResolveServiceEnvReferencesAndCycleDetection(t *testing.T) {
	shared := map[string]string{
		"NODE_ENV": "production",
		"PORT":     "8080",
	}
	services := map[string]map[string]string{
		"web": {
			"PORT":         "3000",
			"DATABASE_URL": "${{postgres.DATABASE_URL}}",
			"API_BASE":     "http://${{api.INTERNAL_HOST}}:8000",
		},
		"postgres": {
			"DATABASE_URL": "postgres://app:secret@postgres:5432/appdb?sslmode=disable",
		},
		"api": {
			"INTERNAL_HOST": "api",
		},
	}

	resolved, err := ResolveServiceEnv("web", shared, services)
	if err != nil {
		t.Fatalf("ResolveServiceEnv: %v", err)
	}
	if resolved["NODE_ENV"] != "production" {
		t.Fatalf("expected shared NODE_ENV, got %q", resolved["NODE_ENV"])
	}
	if resolved["PORT"] != "3000" {
		t.Fatalf("expected service override PORT=3000, got %q", resolved["PORT"])
	}
	if resolved["DATABASE_URL"] != "postgres://app:secret@postgres:5432/appdb?sslmode=disable" {
		t.Fatalf("unexpected DATABASE_URL: %q", resolved["DATABASE_URL"])
	}
	if resolved["API_BASE"] != "http://api:8000" {
		t.Fatalf("unexpected API_BASE: %q", resolved["API_BASE"])
	}

	// Circular reference must fail cleanly.
	services["a"] = map[string]string{"X": "${{b.Y}}"}
	services["b"] = map[string]string{"Y": "${{a.X}}"}
	if _, err := ResolveServiceEnv("a", shared, services); err == nil {
		t.Fatal("expected cycle error")
	}
}
