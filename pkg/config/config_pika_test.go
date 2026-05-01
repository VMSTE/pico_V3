package config

import (
	"encoding/json"
	"testing"
)

func TestConfirmMode_String(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`"always"`), &m); err != nil {
		t.Fatal(err)
	}
	if m != ConfirmAlways {
		t.Errorf("want ConfirmAlways, got %q", m)
	}
}

func TestConfirmMode_Bool(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`true`), &m); err != nil {
		t.Fatal(err)
	}
	if m != ConfirmAlways {
		t.Errorf("want ConfirmAlways for true, got %q", m)
	}
	if err := json.Unmarshal([]byte(`false`), &m); err != nil {
		t.Fatal(err)
	}
	if m != ConfirmNever {
		t.Errorf("want ConfirmNever for false, got %q", m)
	}
}

func TestConfirmMode_Invalid(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`42`), &m); err == nil {
		t.Fatal("expected error for numeric input")
	}
}

func TestSecurityConfig_JSON(t *testing.T) {
	input := `{
		"dangerous_ops": {
			"ops": {
				"deploy.request": {"level": "critical", "confirm": "always"},