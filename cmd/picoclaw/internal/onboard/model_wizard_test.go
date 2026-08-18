package onboard

import (
	"bufio"
	"strings"
	"testing"
)

// D-AUDIT-99: wizard must write provider and accept unlisted ids on confirm.

func TestPickWizardModel_AcceptsListedID(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("stepfun/step-3.5-flash\n"))
	got := pickWizardModel(r, []string{"a/a", "stepfun/step-3.5-flash"})
	if got != "stepfun/step-3.5-flash" {
		t.Fatalf("got %q, want listed id", got)
	}
}

func TestPickWizardModel_UnlistedIDUseAnyway(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("stepfun/step-3.5-flash:free\ny\n"))
	got := pickWizardModel(r, []string{"a/a"})
	if got != "stepfun/step-3.5-flash:free" {
		t.Fatalf("got %q, want unlisted id after confirm", got)
	}
}

func TestPickWizardModel_UnlistedIDDeclined(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("custom/x\nn\na/a\n"))
	got := pickWizardModel(r, []string{"a/a"})
	if got != "a/a" {
		t.Fatalf("got %q, want fallback to listed id after decline", got)
	}
}

func TestPickWizardModel_ByNumber(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("2\n"))
	got := pickWizardModel(r, []string{"a/a", "b/b"})
	if got != "b/b" {
		t.Fatalf("got %q, want b/b", got)
	}
}
