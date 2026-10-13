package contract

import (
	"strings"
	"testing"
)

func enabledInlineSettings() ClassifierSettings {
	return ClassifierSettings{
		Enabled:          true,
		ClassifierSource: ClassifierSourceInline,
		Inline: &ClassifierInlineSource{
			Protocol:      ClassifierProtocolJev,
			Endpoint:      "https://api.typesafe.ai/v1/systemone",
			CredentialRef: ClassifierCredentialRef,
			Model:         "jev-latest",
		},
	}
}

func TestClassifierSettingsValidate(t *testing.T) {
	if err := enabledInlineSettings().Validate(); err != nil {
		t.Fatalf("valid enabled settings: %v", err)
	}
	if err := (ClassifierSettings{Enabled: false, ClassifierSource: ClassifierSourceInline}).Validate(); err != nil {
		t.Fatalf("disabled draft: %v", err)
	}
	providerDraft := ClassifierSettings{
		Enabled:          false,
		ClassifierSource: ClassifierSourceProviderRef,
		ProviderRef:      &ClassifierProviderRef{Ref: "local://classifier-profile/x"},
	}
	if err := providerDraft.Validate(); err != nil {
		t.Fatalf("disabled provider draft: %v", err)
	}
	providerDraft.Enabled = true
	if err := providerDraft.Validate(); err != ErrClassifierSourceNotSupported {
		t.Fatalf("enabled provider ref = %v, want ErrClassifierSourceNotSupported", err)
	}
}

func TestClassifierInlineSourceValidate(t *testing.T) {
	base := *enabledInlineSettings().Inline
	tests := []struct {
		name   string
		mutate func(*ClassifierInlineSource)
	}{
		{"bad protocol", func(s *ClassifierInlineSource) { s.Protocol = "bedrock" }},
		{"ftp endpoint", func(s *ClassifierInlineSource) { s.Endpoint = "ftp://api.example" }},
		{"userinfo endpoint", func(s *ClassifierInlineSource) { s.Endpoint = "https://key@api.example" }},
		{"query endpoint", func(s *ClassifierInlineSource) { s.Endpoint = "https://api.example?x=1" }},
		{"fragment endpoint", func(s *ClassifierInlineSource) { s.Endpoint = "https://api.example#f" }},
		{"foreign credential", func(s *ClassifierInlineSource) { s.CredentialRef = "local://service/svc" }},
		{"bad fallback", func(s *ClassifierInlineSource) { s.Fallback = "panic" }},
		{"threshold above one", func(s *ClassifierInlineSource) { s.LowConfidenceThreshold = float64Ptr(1.5) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestClassifierPromptPlaceholderValidation(t *testing.T) {
	if err := ValidateClassifierPrompt("choose from {{labels}} only"); err != nil {
		t.Fatalf("labels placeholder rejected: %v", err)
	}
	if err := ValidateClassifierPrompt("choose {{labels}} then {{labels}}"); err != nil {
		t.Fatalf("repeated labels placeholder rejected: %v", err)
	}
	if err := ValidateClassifierPrompt("unclosed {{ never mind"); err != nil {
		t.Fatalf("unclosed braces rejected: %v", err)
	}
	if err := ValidateClassifierPrompt("unknown {{labels_upper}}"); err == nil {
		t.Fatal("unknown placeholder accepted")
	}
}

func float64Ptr(value float64) *float64 { return &value }

// Group overrides tighten or replace the global values; timeout may only
// shrink and unknown enum values fall back to the global setting.
func TestClassifierResolveCallPlanPrecedence(t *testing.T) {
	settings := enabledInlineSettings()
	timeout := 1500
	threshold := 0.8
	settings.Inline.TimeoutMS = &timeout
	settings.Inline.LowConfidenceThreshold = &threshold
	settings.Inline.Fallback = ClassifierFallbackGeneral
	settings.Inline.Prompt = "pick {{labels}}"

	plan := settings.ResolveCallPlan(nil)
	if plan.TimeoutMS != 1500 || plan.LowConfidenceThreshold != 0.8 || plan.Fallback != ClassifierFallbackGeneral {
		t.Fatalf("global plan = %+v", plan)
	}
	if !strings.Contains(plan.Prompt, "general, research, coding, architect") {
		t.Fatalf("labels substitution missing: %q", plan.Prompt)
	}

	group := ModelRoutingGroup{
		TaskTypeLabels:      []string{"alpha", "beta"},
		ClassifierPrompt:    "choose {{labels}}",
		ClassifierTimeoutMS: intPtrClassifier(500),
		ClassifierFallback:  "not-an-enum",
	}
	groupPlan := settings.ResolveCallPlan(&group)
	if groupPlan.TimeoutMS != 500 {
		t.Fatalf("group timeout = %d, want 500", groupPlan.TimeoutMS)
	}
	if groupPlan.Fallback != ClassifierFallbackGeneral {
		t.Fatalf("unknown group fallback must fall back to global: %q", groupPlan.Fallback)
	}
	if !strings.Contains(groupPlan.Prompt, "alpha, beta") {
		t.Fatalf("custom labels missing: %q", groupPlan.Prompt)
	}

	loose := group
	loose.ClassifierTimeoutMS = intPtrClassifier(4900)
	if plan := settings.ResolveCallPlan(&loose); plan.TimeoutMS != 1500 {
		t.Fatalf("group may only tighten the timeout, got %d", plan.TimeoutMS)
	}

	// The inline budget stays exactly 1000/5000 even as other sources gain
	// their own; assert both so a new source cannot silently move them.
	unsetInline := enabledInlineSettings()
	if timeout := unsetInline.EffectiveTimeoutMS(); timeout != 1000 {
		t.Fatalf("inline default timeout = %d, want 1000", timeout)
	}
	overInlineCap := enabledInlineSettings()
	aboveInlineCap := 5001
	overInlineCap.Inline.TimeoutMS = &aboveInlineCap
	if err := overInlineCap.Validate(); err == nil {
		t.Fatal("inline timeout above its 5000 cap must be rejected")
	}
}

// T6: the existing_model source carries an independent budget — default
// 15000, cap 60000 — while inline keeps 1000/5000. Both sources' numbers are
// asserted side by side so moving either one fails this test.
func TestClassifierExistingModelBudget(t *testing.T) {
	existingModel := ClassifierSettings{
		Enabled:          true,
		ClassifierSource: ClassifierSourceExistingModel,
		ExistingModel:    &ClassifierExistingModelSource{ServiceID: "service_01", Model: "claude-haiku"},
	}
	if err := existingModel.Validate(); err != nil {
		t.Fatalf("valid enabled existing_model settings: %v", err)
	}
	// Unset timeout resolves to the source default, not inline's 1000.
	if timeout := existingModel.EffectiveTimeoutMS(); timeout != 15000 {
		t.Fatalf("existing_model default timeout = %d, want 15000", timeout)
	}
	if plan := existingModel.ResolveCallPlan(nil); plan.TimeoutMS != 15000 {
		t.Fatalf("existing_model plan timeout = %d, want 15000", plan.TimeoutMS)
	}

	// The cap accepts 60000 and rejects 60001.
	atCap := 60000
	existingModel.ExistingModel.TimeoutMS = &atCap
	if err := existingModel.Validate(); err != nil {
		t.Fatalf("existing_model timeout 60000 rejected: %v", err)
	}
	if timeout := existingModel.EffectiveTimeoutMS(); timeout != 60000 {
		t.Fatalf("existing_model explicit timeout = %d, want 60000", timeout)
	}
	overCap := 60001
	existingModel.ExistingModel.TimeoutMS = &overCap
	if err := existingModel.Validate(); err == nil {
		t.Fatal("existing_model timeout 60001 must be rejected")
	}

	// A group may only tighten, whatever the source cap would allow.
	existingModel.ExistingModel.TimeoutMS = nil
	explicit := 30000
	global := ClassifierSettings{
		Enabled:          true,
		ClassifierSource: ClassifierSourceExistingModel,
		ExistingModel:    &ClassifierExistingModelSource{ServiceID: "service_01", Model: "claude-haiku", TimeoutMS: &explicit},
	}
	tighten := ModelRoutingGroup{ClassifierTimeoutMS: intPtrClassifier(20000)}
	if plan := global.ResolveCallPlan(&tighten); plan.TimeoutMS != 20000 {
		t.Fatalf("group 20000 against global 30000 = %d, want 20000 (tighten)", plan.TimeoutMS)
	}
	enlarge := ModelRoutingGroup{ClassifierTimeoutMS: intPtrClassifier(40000)}
	if plan := existingModel.ResolveCallPlan(&enlarge); plan.TimeoutMS != 15000 {
		t.Fatalf("group 40000 against global default 15000 = %d, want 15000 (no enlarge)", plan.TimeoutMS)
	}

	// One ResolveCallPlan call with both source blocks present: each source
	// reads only its own budget.
	inlineBudget := 1500
	existingBudget := 45000
	mixed := ClassifierSettings{
		Enabled:          true,
		ClassifierSource: ClassifierSourceExistingModel,
		Inline: &ClassifierInlineSource{
			Protocol:      ClassifierProtocolJev,
			Endpoint:      "https://api.typesafe.ai/v1/systemone",
			CredentialRef: ClassifierCredentialRef,
			Model:         "jev-latest",
			TimeoutMS:     &inlineBudget,
		},
		ExistingModel: &ClassifierExistingModelSource{ServiceID: "service_01", Model: "claude-haiku", TimeoutMS: &existingBudget},
	}
	if plan := mixed.ResolveCallPlan(nil); plan.TimeoutMS != 45000 {
		t.Fatalf("existing_model plan picked up the inline budget: %d, want 45000", plan.TimeoutMS)
	}
	shrunk := ModelRoutingGroup{ClassifierTimeoutMS: intPtrClassifier(20000)}
	if plan := mixed.ResolveCallPlan(&shrunk); plan.TimeoutMS != 20000 {
		t.Fatalf("mixed plan with group 20000 = %d, want 20000 (tighten)", plan.TimeoutMS)
	}
	mirrored := enabledInlineSettings()
	mirrored.ExistingModel = &ClassifierExistingModelSource{ServiceID: "service_01", Model: "claude-haiku", TimeoutMS: &existingBudget}
	if timeout := mirrored.EffectiveTimeoutMS(); timeout != 1000 {
		t.Fatalf("inline plan picked up the existing_model budget: %d, want 1000", timeout)
	}

	// Side by side with inline: its budget is still 1000/5000.
	inlineSide := enabledInlineSettings()
	if timeout := inlineSide.EffectiveTimeoutMS(); timeout != 1000 {
		t.Fatalf("inline default timeout = %d, want 1000", timeout)
	}
	inlineOver := enabledInlineSettings()
	aboveInlineCap := 5001
	inlineOver.Inline.TimeoutMS = &aboveInlineCap
	if err := inlineOver.Validate(); err == nil {
		t.Fatal("inline timeout above 5000 must be rejected")
	}
}

func intPtrClassifier(value int) *int { return &value }
