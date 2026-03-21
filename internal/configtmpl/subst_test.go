package configtmpl

import (
	"encoding/json"
	"os"
	"testing"
)

func TestApply_GlobalAndEnvAndEscape(t *testing.T) {
	t.Setenv("CONFIGTMPL_TEST_ENV", "env-value")

	input := json.RawMessage(`{
		"global_ref": "${global:TOKEN}",
		"env_ref": "${env:CONFIGTMPL_TEST_ENV}",
		"escaped": "\\${global:TOKEN}",
		"unknown": "${global:UNKNOWN}",
		"legacy": "${TOKEN}",
		"arr": ["${global:TOKEN}"]
	}`)

	out, err := Apply(input, map[string]string{"TOKEN": "global-value"})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output error = %v", err)
	}

	if got["global_ref"] != "global-value" {
		t.Fatalf("global_ref = %v, want %v", got["global_ref"], "global-value")
	}
	if got["env_ref"] != "env-value" {
		t.Fatalf("env_ref = %v, want %v", got["env_ref"], "env-value")
	}
	if got["escaped"] != "${global:TOKEN}" {
		t.Fatalf("escaped = %v, want %v", got["escaped"], "${global:TOKEN}")
	}
	if got["unknown"] != "${global:UNKNOWN}" {
		t.Fatalf("unknown = %v, want %v", got["unknown"], "${global:UNKNOWN}")
	}
	// Legacy format is no longer supported and should stay unchanged.
	if got["legacy"] != "${TOKEN}" {
		t.Fatalf("legacy = %v, want %v", got["legacy"], "${TOKEN}")
	}

	arr, ok := got["arr"].([]any)
	if !ok || len(arr) != 1 || arr[0] != "global-value" {
		t.Fatalf("arr = %v, want [global-value]", got["arr"])
	}
}

func TestApply_InvalidInput(t *testing.T) {
	_, err := Apply(json.RawMessage(`[]`), nil)
	if err == nil {
		t.Fatalf("expected error for non-object JSON")
	}
}

func TestApply_MissingEnvKeepsPlaceholder(t *testing.T) {
	_ = os.Unsetenv("CONFIGTMPL_MISSING_ENV")
	out, err := Apply(json.RawMessage(`{"v":"${env:CONFIGTMPL_MISSING_ENV}"}`), nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output error = %v", err)
	}
	if got["v"] != "${env:CONFIGTMPL_MISSING_ENV}" {
		t.Fatalf("v = %v, want placeholder unchanged", got["v"])
	}
}
