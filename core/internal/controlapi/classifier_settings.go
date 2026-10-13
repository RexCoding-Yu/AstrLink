package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier/cloud"
	"github.com/QuantumNous/astrlink/core/internal/modelrouting"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

// ClassifierSettingsPath is the singleton external-classifier resource. It
// deliberately does not extend the routing-settings document or its
// whitelists.
const ClassifierSettingsPath = "/control/v1/routing/classifier"

// classifierCredentialRef is the single V1 credential slot.
const classifierCredentialRef = secretstore.Ref(contract.ClassifierCredentialRef)

// dryRunPreviewRunes bounds the sanitized preview returned to the UI.
const dryRunPreviewRunes = 400

// dryRunSlots allows one dry-run at a time: each one is a real cloud call.
var dryRunSlots = make(chan struct{}, 1)

func (handler *Handler) classifierResource(writer http.ResponseWriter, request *http.Request) {
	if handler.classifierSettings == nil {
		writeError(writer, http.StatusServiceUnavailable, "classifier_settings_unavailable", "classifier settings are unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		settings, err := handler.classifierSettings.GetClassifierSettings(request.Context())
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		handler.writeClassifierSettings(writer, request, settings)
	case http.MethodPut:
		var settings contract.ClassifierSettings
		if !decodeControlJSON(writer, request, &settings) {
			return
		}
		if err := settings.Validate(); err != nil {
			if errors.Is(err, contract.ErrClassifierSourceNotSupported) {
				writeError(writer, http.StatusUnprocessableEntity, "classifier_source_not_supported", err.Error())
				return
			}
			writeError(writer, http.StatusUnprocessableEntity, "invalid_classifier_settings", err.Error())
			return
		}
		if err := handler.classifierSettings.UpdateClassifierSettings(request.Context(), settings); err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		handler.writeClassifierSettings(writer, request, settings)
	default:
		writer.Header().Set("Allow", "GET, PUT")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and PUT are allowed")
	}
}

func (handler *Handler) writeClassifierSettings(writer http.ResponseWriter, request *http.Request, settings contract.ClassifierSettings) {
	configured := false
	if secrets, ok := handler.serviceStore.(secretstore.SecretStore); ok {
		value, err := secrets.Get(request.Context(), classifierCredentialRef)
		clear(value)
		if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not read classifier credential status")
			return
		}
		configured = err == nil
	}
	writeJSON(writer, http.StatusOK, classifierSettingsResponse{
		ClassifierSettings:   settings,
		CredentialConfigured: configured,
	})
}

type classifierSettingsResponse struct {
	contract.ClassifierSettings
	CredentialConfigured bool `json:"credential_configured"`
}

func (handler *Handler) classifierSubResource(writer http.ResponseWriter, request *http.Request) {
	action, _, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, ClassifierSettingsPath+"/"), "/")
	switch action {
	case "credential":
		handler.classifierCredentialResource(writer, request)
	case "dry-run":
		handler.classifierDryRun(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not_found", "classifier endpoint not found")
	}
}

func (handler *Handler) classifierCredentialResource(writer http.ResponseWriter, request *http.Request) {
	secrets, ok := handler.serviceStore.(secretstore.SecretStore)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "credential store is unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		value, err := secrets.Get(request.Context(), classifierCredentialRef)
		defer clear(value)
		if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not read classifier credential status")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"configured": err == nil})
	case http.MethodPut:
		var input struct {
			Secret string `json:"secret"`
		}
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		if len(input.Secret) == 0 || len(input.Secret) > 16384 || strings.ContainsAny(input.Secret, "\r\n") {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_credential", "API key must contain 1 to 16384 bytes without newlines")
			return
		}
		value := []byte(input.Secret)
		defer clear(value)
		if err := secrets.Put(request.Context(), classifierCredentialRef, value); err != nil {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not save classifier credential")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"configured": true})
	case http.MethodDelete:
		if err := secrets.Delete(request.Context(), classifierCredentialRef); err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not delete classifier credential")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"configured": false})
	default:
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET, PUT or DELETE")
	}
}

type classifierDryRunRequest struct {
	Text    string `json:"text"`
	GroupID string `json:"group_id,omitempty"`
}

type classifierDryRunDecision struct {
	MemberID    string `json:"member_id"`
	ServiceID   string `json:"service_id"`
	Model       string `json:"model"`
	RuleID      string `json:"rule_id,omitempty"`
	UsedDefault bool   `json:"used_default"`
}

type classifierDryRunResponse struct {
	Configured       bool                                    `json:"configured"`
	Category         string                                  `json:"category,omitempty"`
	Confidence       float64                                 `json:"confidence,omitempty"`
	FallbackReason   string                                  `json:"fallback_reason,omitempty"`
	Privacy          string                                  `json:"privacy,omitempty"`
	DurationMS       int                                     `json:"duration_ms"`
	SanitizedPreview string                                  `json:"sanitized_preview,omitempty"`
	Decision         *classifierDryRunDecision               `json:"decision,omitempty"`
	Fields           []contract.ModelRoutingFieldObservation `json:"fields,omitempty"`
}

// classifierDryRun previews one classification locally: the text passes the
// same privacy filter as production, then the configured provider is asked
// once. No inference upstream is contacted and nothing is persisted.
func (handler *Handler) classifierDryRun(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if handler.classifierSettings == nil {
		writeError(writer, http.StatusServiceUnavailable, "classifier_settings_unavailable", "classifier settings are unavailable")
		return
	}
	select {
	case dryRunSlots <- struct{}{}:
		defer func() { <-dryRunSlots }()
	default:
		writeError(writer, http.StatusTooManyRequests, "dry_run_busy", "another dry-run is in progress")
		return
	}
	var input classifierDryRunRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > 64*1024 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_dry_run", "text must contain 1 to 65536 runes")
		return
	}
	settings, err := handler.classifierSettings.GetClassifierSettings(request.Context())
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	response := classifierDryRunResponse{Configured: settings.CloudConfigured()}
	if !response.Configured {
		response.FallbackReason = autoclassifier.FallbackUnavailable
		writeJSON(writer, http.StatusOK, response)
		return
	}
	sanitized, privacyState := handler.dryRunPrivacy(request.Context(), input.Text)
	response.Privacy = privacyState
	response.SanitizedPreview = boundedDryRunPreview(sanitized)
	if privacyState == "blocked" || privacyState == "inspection_failed" {
		response.FallbackReason = "privacy_" + privacyState
		writeJSON(writer, http.StatusOK, response)
		return
	}
	group := handler.dryRunGroup(request.Context(), input.GroupID)
	plan := settings.ResolveCallPlan(group)
	secrets, _ := handler.serviceStore.(secretstore.SecretStore)
	if secrets == nil {
		response.FallbackReason = autoclassifier.FallbackUnavailable
		writeJSON(writer, http.StatusOK, response)
		return
	}
	secret, secretErr := secrets.Get(request.Context(), classifierCredentialRef)
	if secretErr != nil {
		response.FallbackReason = autoclassifier.FallbackUnavailable
		writeJSON(writer, http.StatusOK, response)
		return
	}
	started := time.Now()
	outcome := cloud.Classify(request.Context(), nil, cloud.Input{
		Protocol:  cloud.Protocol(plan.Protocol),
		Endpoint:  plan.Endpoint,
		APIKey:    secret,
		Model:     plan.Model,
		Prompt:    plan.Prompt,
		Labels:    plan.Labels,
		Text:      sanitized,
		TimeoutMS: plan.TimeoutMS,
	})
	response.DurationMS = int(time.Since(started).Milliseconds())
	response.Confidence = outcome.Confidence
	// The dry-run fills the same routing-side carrier as the inference
	// path's cloud classifier (modelRoutingTaskTypeCloud): success requires a
	// label the group accepts and enough confidence, low confidence stays a
	// code and never a fallback, and failures go through the shared mapping.
	// Facts and observations then derive from that carrier through the one
	// conversion in modelrouting, never from a synthesized single value.
	taskType := modelrouting.TaskTypeOutcome{
		ClassifierID:           string(outcome.ClassifierID),
		Candidates:             outcome.Candidates,
		LowConfidenceThreshold: plan.LowConfidenceThreshold,
	}
	if outcome.OK() {
		if group != nil && !group.ValidTaskTypeLabel(outcome.Category) {
			taskType.Code = "classifier_invalid_output"
		} else if outcome.Confidence < plan.LowConfidenceThreshold {
			taskType.Code = "classifier_low_confidence"
		} else {
			taskType.Value = outcome.Category
			taskType.Confidence = outcome.Confidence
		}
	} else {
		// The dry-run preview reports failures through the same shared
		// mapping as production routing: stable record codes instead of raw
		// provider reasons, and the configured fallback (error records
		// classifier_error, general fills the label when the group accepts
		// it) applies identically in both consumers.
		var groupValue contract.ModelRoutingGroup
		if group != nil {
			groupValue = *group
		}
		failure := autoclassifier.FailureTaskType(plan.Fallback, groupValue, outcome, response.DurationMS)
		taskType.Value = failure.Value
		taskType.Code = failure.Code
	}
	response.Category = taskType.Value
	response.FallbackReason = taskType.Code
	if group != nil {
		now := time.Now()
		facts := contract.ModelRoutingFacts{
			Protocol:    string(contract.ProtocolOpenAIChat),
			MediaKind:   "none",
			Streaming:   boolPtrDryRun(false),
			LiteralText: sanitized,
			TaskType:    taskType.Value,
			SystemTime:  &now,
		}
		facts.TaskTypeCandidates = modelrouting.TaskTypeCandidateLabels(taskType, *group)
		selection := modelrouting.Evaluate(*group, facts)
		candidateObservations := modelrouting.TaskTypeCandidateObservations(taskType, *group, selection.TaskTypeCandidateIndex)
		if taskType.ClassifierID != "" || taskType.Confidence > 0 || candidateObservations != nil {
			for index := range selection.Fields {
				if selection.Fields[index].Source == "classifier" {
					selection.Fields[index].ClassifierID = taskType.ClassifierID
					selection.Fields[index].ClassifierConfidence = taskType.Confidence
					selection.Fields[index].Candidates = candidateObservations
				}
			}
		}
		response.Fields = selection.Fields
		response.Decision = &classifierDryRunDecision{
			MemberID:    selection.Member.ID,
			ServiceID:   string(selection.Member.ServiceID),
			Model:       selection.Member.Model,
			RuleID:      selection.RuleID,
			UsedDefault: selection.UsedDefault,
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func boolPtrDryRun(value bool) *bool { return &value }

// dryRunPrivacy applies the production privacy filter to the dry-run text by
// wrapping it in the chat envelope the detector understands. The sanitized
// user text returns for classification; the state names the outcome.
func (handler *Handler) dryRunPrivacy(ctx context.Context, text string) (string, string) {
	if handler.privacyFilter == nil {
		return text, "off"
	}
	policy, err := handler.privacyFilter.ResolvePolicy(ctx, privacy.Scope{})
	if err != nil {
		return "", "unavailable"
	}
	if !policy.Enabled {
		return text, "off"
	}
	envelope, err := json.Marshal(map[string]any{
		"model":    "astrlink-dry-run",
		"messages": []map[string]string{{"role": "user", "content": text}},
	})
	if err != nil {
		return "", "inspection_failed"
	}
	result, err := handler.privacyFilter.Inspect(ctx, policy, contract.ProtocolOpenAIChat, envelope)
	if err != nil {
		return "", "inspection_failed"
	}
	switch result.Decision {
	case privacy.DecisionBlock:
		return "", "blocked"
	case privacy.DecisionRedact:
		return extractDryRunText(result.Body), "redact"
	default:
		return text, string(result.Decision)
	}
}

func extractDryRunText(body []byte) string {
	var envelope struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	for index := len(envelope.Messages) - 1; index >= 0; index-- {
		if envelope.Messages[index].Role == "user" {
			return envelope.Messages[index].Content
		}
	}
	return ""
}

func boundedDryRunPreview(text string) string {
	runes := []rune(text)
	if len(runes) > dryRunPreviewRunes {
		return string(runes[:dryRunPreviewRunes]) + "…"
	}
	return text
}

func (handler *Handler) dryRunGroup(ctx context.Context, groupID string) *contract.ModelRoutingGroup {
	if groupID == "" || handler.routingSettings == nil {
		return nil
	}
	settings, err := handler.routingSettings.GetRoutingSettings(ctx)
	if err != nil {
		return nil
	}
	for index := range settings.ModelRoutingGroups {
		if settings.ModelRoutingGroups[index].ID == groupID {
			group := settings.ModelRoutingGroups[index]
			return &group
		}
	}
	return nil
}
