package ingress

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier/cloud"
	"github.com/QuantumNous/astrlink/core/internal/classifieroutput"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/modelrouting"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Keep V1 binding retention bounded without expanding the persisted settings
// contract. One hour covers normal conversational turn gaps while limiting
// stale member selection after a session is abandoned.
const defaultModelRoutingBindingTTL = time.Hour

type modelRoutingHTTPError struct {
	status  int
	code    string
	message string
}

func (err *modelRoutingHTTPError) Error() string { return err.message }

func (handler *Handler) applyModelRouting(ctx context.Context, session *recordSession, classified Request, settings contract.RoutingSettings) (Request, error) {
	if classified.Model == "" || !strings.HasPrefix(classified.Model, contract.ModelRoutingModelPrefix) || classified.Protocol.IsModelDiscovery() {
		return classified, nil
	}
	if classified.Model == contract.AstrLinkAutoModelID {
		return classified, nil
	}
	groupID := strings.TrimPrefix(classified.Model, contract.ModelRoutingModelPrefix)
	var group *contract.ModelRoutingGroup
	for index := range settings.ModelRoutingGroups {
		if settings.ModelRoutingGroups[index].ID == groupID {
			group = &settings.ModelRoutingGroups[index]
			break
		}
	}
	if group == nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_not_found", message: fmt.Sprintf("model routing group %q was not found", groupID)}
	}
	if err := handler.validateModelRoutingGroup(ctx, session, *group); err != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: err.Error()}
	}
	version, err := contract.ModelRoutingSnapshotVersion(settings.ModelRoutingGroups)
	if err != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: err.Error()}
	}
	if turn := responsesWSTurnFromContext(ctx); turn != nil && turn.session.serviceID != "" && turn.session.routingModel != "" {
		for index := range group.Members {
			member := &group.Members[index]
			if member.ServiceID == turn.session.serviceID && member.Model == turn.session.routingModel {
				decision := modelRoutingDecisionFromBinding(contract.ModelRoutingBinding{GroupID: group.ID, MemberID: member.ID, ServiceID: member.ServiceID, Model: member.Model, ConfigSnapshotVersion: version}, classified.Model, version)
				decision.SelectionSource = contract.ModelRoutingSelectionWebSocket
				classified.ModelRoutingMember = member
				pinModelRoutingWebSocket(ctx, member.Model)
				if session != nil {
					session.noteModelRouting(ctx, decision)
				}
				return classified, nil
			}
		}
		return classified, &modelRoutingHTTPError{status: 409, code: "model_routing_binding_conflict", message: "Responses WebSocket is bound to a different model routing member"}
	}
	if classified.PreviousResponseID != "" && (classified.Protocol == contract.ProtocolOpenAIResponses || classified.Protocol == contract.ProtocolOpenAIResponsesCompact) {
		affinity, found, affinityErr := handler.loadResponseAffinity(ctx, classified.PreviousResponseID)
		if affinityErr != nil {
			return classified, &modelRoutingHTTPError{status: 409, code: "model_routing_binding_conflict", message: affinityErr.Error()}
		}
		if found {
			var member *contract.ModelRoutingMember
			for index := range group.Members {
				if group.Members[index].ServiceID == affinity.ServiceID && group.Members[index].Model == affinity.UpstreamModel {
					member = &group.Members[index]
					break
				}
			}
			if member == nil {
				return classified, &modelRoutingHTTPError{status: 409, code: "model_routing_binding_conflict", message: "previous response belongs to a different model routing member"}
			}
			decision := modelRoutingDecisionFromBinding(contract.ModelRoutingBinding{GroupID: group.ID, MemberID: member.ID, ServiceID: member.ServiceID, Model: member.Model, ConfigSnapshotVersion: version}, classified.Model, version)
			decision.SelectionSource = contract.ModelRoutingSelectionResponseAffinity
			classified.ModelRoutingMember = member
			pinModelRoutingWebSocket(ctx, member.Model)
			if session != nil {
				session.noteModelRouting(ctx, decision)
			}
			return classified, nil
		}
	}
	if binding, ok, bindingErr := handler.loadModelRoutingBinding(ctx, session, classified); bindingErr != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: bindingErr.Error()}
	} else if ok {
		decision := modelRoutingDecisionFromBinding(binding, classified.Model, version)
		classified.ModelRoutingMember = &contract.ModelRoutingMember{ID: binding.MemberID, ServiceID: binding.ServiceID, Model: binding.Model}
		pinModelRoutingWebSocket(ctx, binding.Model)
		if session != nil {
			session.noteModelRouting(ctx, decision)
		}
		return classified, nil
	}
	if !group.Enabled {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_disabled", message: fmt.Sprintf("model routing group %q is disabled", groupID)}
	}
	facts := modelRoutingFacts(classified, session)
	var taskType modelrouting.TaskTypeOutcome
	classifierSettings := handler.loadClassifierSettings(ctx, session)
	dimensions := classifierSettings.ResolveCallPlan(group).Dimensions
	if modelRoutingNeedsClassification(*group, dimensions) {
		taskType = handler.modelRoutingTaskType(ctx, classified, *group, classifierSettings)
		facts.TaskType = taskType.Value
		facts.TaskTypeCandidates = modelrouting.TaskTypeCandidateLabels(taskType, *group)
	}
	// Custom dimensions ride on the same attempt as task_type. An empty
	// outcome leaves the map nil, which is the single-dimension configuration:
	// every rule that tests a built-in field evaluates exactly as before.
	facts.Dimensions = modelrouting.DimensionFacts(taskType)
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
	if selection.Member.ID == "" {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: "model routing group default member is invalid"}
	}
	decision := contract.ModelRoutingDecision{
		SchemaVersion:         1,
		VirtualModelID:        classified.Model,
		GroupID:               group.ID,
		ConfigSnapshotVersion: version,
		SelectionSource:       selection.SelectionSource,
		RuleID:                selection.RuleID,
		RulePriority:          selection.RulePriority,
		UsedDefault:           selection.UsedDefault,
		Fields:                selection.Fields,
		MemberID:              selection.Member.ID,
		MemberServiceID:       selection.Member.ServiceID,
		MemberModel:           selection.Member.Model,
		FallbackReason:        taskType.Code,
		Execution:             contract.ModelRoutingExecution{Status: contract.ModelRoutingExecutionPending},
		PrivacyAttempts:       []contract.ModelRoutingPrivacyAttempt{},
		ClassifierDurationMS:  taskType.DurationMS,
	}
	if err := decision.Validate(); err != nil {
		return classified, &modelRoutingHTTPError{status: 422, code: "model_routing_group_invalid", message: err.Error()}
	}
	classified.ModelRoutingMember = &contract.ModelRoutingMember{ID: selection.Member.ID, ServiceID: selection.Member.ServiceID, Model: selection.Member.Model}
	pinModelRoutingWebSocket(ctx, selection.Member.Model)
	if session != nil {
		session.noteModelRouting(ctx, decision)
	}
	return classified, nil
}

// validateModelRoutingGroup validates a group against the dimension set the
// classifier settings declare. A rule may only name a user-defined dimension
// when that dimension is actually declared, and the group document itself
// never carries the dimensions, so the check needs both halves.
//
// The cheap validation runs first and is the whole check for every group whose
// rules name built-in fields only — which is every group that has ever been
// stored. Only a group that fails without dimensions pays for the classifier
// read, and the read is skipped entirely when classifier settings are not
// wired (a build without the classifier sub-resource), where an undeclared
// dimension correctly stays unsupported. Resolving the dimensions before the
// sticky-session and affinity short-circuits would add a store read to the
// hot path of every pinned request for the benefit of a document that was
// already validated on save.
func (handler *Handler) validateModelRoutingGroup(ctx context.Context, session *recordSession, group contract.ModelRoutingGroup) error {
	err := group.Validate()
	if err == nil || handler.classifierSettings == nil {
		return err
	}
	dimensions := handler.loadClassifierSettings(ctx, session).Dimensions
	if len(dimensions) == 0 {
		return err
	}
	return group.ValidateWithDimensions(dimensions)
}

func (handler *Handler) resolveMemberCandidates(ctx context.Context, request endpoint.ResolveRequest, member contract.ModelRoutingMember) ([]endpoint.Resolved, error) {
	request.Model = member.Model
	request.AllCandidates = true
	if resolver, ok := handler.resolver.(endpoint.ExactTargetResolver); ok {
		return resolver.ResolveMemberCandidates(ctx, request, member.ServiceID, member.Model)
	}
	candidates, err := handler.resolveCandidates(ctx, request)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		if candidate.CanonicalService().ID == member.ServiceID && (candidate.UpstreamModel == "" || candidate.UpstreamModel == member.Model) {
			candidate.UpstreamModel = member.Model
			return []endpoint.Resolved{candidate}, nil
		}
	}
	return nil, endpoint.ErrNoEndpoint
}

func cloneModelRoutingFields(fields []contract.ModelRoutingFieldObservation) []contract.ModelRoutingFieldObservation {
	if fields == nil {
		return nil
	}
	return append([]contract.ModelRoutingFieldObservation{}, fields...)
}

func cloneModelRoutingPrivacyAttempts(attempts []contract.ModelRoutingPrivacyAttempt) []contract.ModelRoutingPrivacyAttempt {
	if attempts == nil {
		return nil
	}
	return append([]contract.ModelRoutingPrivacyAttempt{}, attempts...)
}

func (session *recordSession) noteModelRoutingPrivacyAttempt(serviceID contract.ServiceID, decision contract.ModelRoutingPrivacyDecision, findings, durationMS int, code string) {
	if session == nil || session.modelRouting == nil {
		return
	}
	if findings < 0 {
		findings = 0
	}
	if durationMS < 0 {
		durationMS = 0
	}
	session.modelRouting.PrivacyAttempts = append(session.modelRouting.PrivacyAttempts, contract.ModelRoutingPrivacyAttempt{AttemptIndex: session.attemptIndex, ServiceID: serviceID, Decision: decision, FindingCount: findings, DurationMS: durationMS, ErrorCode: code})
}

func (session *recordSession) updateModelRoutingExecution(status contract.ModelRoutingExecutionStatus, code string) {
	if session == nil || session.modelRouting == nil {
		return
	}
	session.modelRouting.Execution.Status = status
	session.modelRouting.Execution.AttemptCount = max(session.attemptIndex, 0)
	session.modelRouting.Execution.ErrorCode = code
	session.modelRouting.ErrorCode = code
}

func (handler *Handler) persistModelRoutingBinding(ctx context.Context, session *recordSession) {
	if handler == nil || session == nil || session.modelRouting == nil || session.status != contract.RequestStatusSucceeded || session.sessionID == "" || handler.requestRecords == nil {
		return
	}
	store, ok := handler.requestRecords.(storage.ModelRoutingBindingStore)
	if !ok {
		return
	}
	principal, ok := modelRoutingBindingPrincipal(session)
	if !ok {
		// Without a stable access-token principal, do not create a shared
		// anonymous namespace that could make same-session IDs cross-talk.
		return
	}
	now := time.Now().UTC()
	binding := contract.ModelRoutingBinding{
		Principal: principal, SessionID: session.sessionID, Protocol: session.classified.Protocol,
		VirtualModelID: session.modelRouting.VirtualModelID, GroupID: session.modelRouting.GroupID,
		MemberID: session.modelRouting.MemberID, ServiceID: session.modelRouting.MemberServiceID,
		Model: session.modelRouting.MemberModel, ConfigSnapshotVersion: session.modelRouting.ConfigSnapshotVersion,
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(defaultModelRoutingBindingTTL).Format(time.RFC3339Nano),
	}
	if err := store.UpsertModelRoutingBinding(ctx, binding); err != nil && session.persistLogf != nil {
		session.persistLogf("model routing binding write failed: %v", err)
	}
}

func (handler *Handler) loadModelRoutingBinding(ctx context.Context, session *recordSession, classified Request) (contract.ModelRoutingBinding, bool, error) {
	if session == nil || session.sessionID == "" || handler.requestRecords == nil {
		return contract.ModelRoutingBinding{}, false, nil
	}
	store, ok := handler.requestRecords.(storage.ModelRoutingBindingStore)
	if !ok {
		return contract.ModelRoutingBinding{}, false, nil
	}
	principal, ok := modelRoutingBindingPrincipal(session)
	if !ok {
		// Without a stable access-token principal, never read another caller's
		// binding from a shared anonymous namespace.
		return contract.ModelRoutingBinding{}, false, nil
	}
	binding, err := store.GetModelRoutingBinding(ctx, principal, session.sessionID, classified.Protocol, classified.Model)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return contract.ModelRoutingBinding{}, false, nil
		}
		return contract.ModelRoutingBinding{}, false, err
	}
	return binding, true, nil
}

func modelRoutingBindingPrincipal(session *recordSession) (string, bool) {
	if session == nil || session.accessTokenID == nil || *session.accessTokenID == "" {
		return "", false
	}
	return string(*session.accessTokenID), true
}

func modelRoutingDecisionFromBinding(binding contract.ModelRoutingBinding, virtualModelID, version string) contract.ModelRoutingDecision {
	if binding.ConfigSnapshotVersion != "" {
		version = binding.ConfigSnapshotVersion
	}
	return contract.ModelRoutingDecision{
		SchemaVersion: 1, VirtualModelID: virtualModelID, GroupID: binding.GroupID,
		ConfigSnapshotVersion: version, SelectionSource: contract.ModelRoutingSelectionGroupBinding,
		UsedDefault: false, Fields: []contract.ModelRoutingFieldObservation{}, MemberID: binding.MemberID,
		MemberServiceID: binding.ServiceID, MemberModel: binding.Model,
		Execution:       contract.ModelRoutingExecution{Status: contract.ModelRoutingExecutionPending},
		PrivacyAttempts: []contract.ModelRoutingPrivacyAttempt{},
	}
}

func modelRoutingFacts(classified Request, session *recordSession) contract.ModelRoutingFacts {
	facts := contract.ModelRoutingFacts{
		Protocol:       string(classified.Protocol),
		MediaKind:      "none",
		Streaming:      routingBoolPtr(classified.Streaming),
		Continuation:   routingBoolPtr(classified.PreviousResponseID != ""),
		InputTextRunes: routingIntPtr(utf8.RuneCountInString(classified.Conversation.LastUserText)),
	}
	// One clock sample per request: system_time leaves read this instant, in
	// the OS local timezone, so all rows in one evaluation see the same time.
	now := time.Now()
	facts.SystemTime = &now
	if classified.routingText != "" {
		facts.LiteralText = classified.routingText
	} else if classified.Conversation.LastUserText != "" {
		facts.LiteralText = classified.Conversation.LastUserText
	} else {
		facts.LiteralText = classified.InputPreview
	}
	facts.UserMessageCount = routingIntPtr(classified.Conversation.UserTurnCount)
	if session != nil && session.turn != nil {
		facts.TurnCount = routingIntPtr(session.turn.Index)
	}
	return facts
}

// loadClassifierSettings reads the external classifier configuration. Any
// failure degrades to the empty (disabled) settings so classification stays
// fail-open: an unreadable classifier must never block routing.
func (handler *Handler) loadClassifierSettings(ctx context.Context, session *recordSession) contract.ClassifierSettings {
	if handler.classifierSettings == nil {
		return contract.ClassifierSettings{}
	}
	settings, err := handler.classifierSettings.GetClassifierSettings(ctx)
	if err != nil {
		if session != nil && session.persistLogf != nil {
			session.persistLogf("classifier settings lookup failed: %v", err)
		}
		return contract.ClassifierSettings{}
	}
	return settings
}

// TaskTypeOutcome lives in the modelrouting package next to the one
// conversion (TaskTypeCandidateLabels / TaskTypeCandidateObservations) the
// inference path and the control-plane dry-run share; every classification
// source below fills that carrier and never derives facts on its own.
func (handler *Handler) modelRoutingTaskType(ctx context.Context, classified Request, group contract.ModelRoutingGroup, settings contract.ClassifierSettings) modelrouting.TaskTypeOutcome {
	// The classification input is the user's actual message, never the
	// sanitized protocol envelope: routingText is the full redacted request
	// body, which is not "one sentence" and routinely embeds JSON.
	text := classified.lastUserText
	if text == "" {
		text = classified.Conversation.LastUserText
	}
	if strings.TrimSpace(text) == "" {
		return modelrouting.TaskTypeOutcome{Code: autoclassifier.FallbackUnavailable}
	}
	if len(text) > 64*1024 {
		text = text[:64*1024]
	}
	if settings.CloudConfigured() {
		return handler.modelRoutingTaskTypeCloud(ctx, text, group, settings)
	}
	if existingModelConfigured(settings) {
		return handler.modelRoutingTaskTypeExistingModel(ctx, text, group, settings)
	}
	if handler.classifier == nil {
		return modelrouting.TaskTypeOutcome{Code: autoclassifier.FallbackUnavailable}
	}
	budget := 300 * time.Millisecond
	if classified.Streaming {
		budget = 100 * time.Millisecond
	}
	start := time.Now()
	classifyCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var outcome autoclassifier.Outcome
	if tryClassifier, ok := handler.classifier.(interface {
		TryClassify(context.Context, string) autoclassifier.Outcome
	}); ok {
		outcome = tryClassifier.TryClassify(classifyCtx, text)
	} else {
		outcome = handler.classifier.Classify(classifyCtx, text)
	}
	result := modelrouting.TaskTypeOutcome{DurationMS: int(time.Since(start).Milliseconds()), ClassifierID: string(outcome.ClassifierID), Candidates: outcome.Candidates}
	if outcome.OK() {
		if group.ValidTaskTypeLabel(outcome.Category) {
			result.Value = outcome.Category
			result.Confidence = outcome.Confidence
			return result
		}
		result.Code = "classifier_invalid_output"
		return result
	}
	if outcome.FallbackReason == autoclassifier.FallbackBusy {
		result.Code = "classifier_busy"
		return result
	}
	if errors.Is(classifyCtx.Err(), context.DeadlineExceeded) || outcome.FallbackReason == autoclassifier.FallbackTimeout {
		result.Code = "classifier_timeout"
		return result
	}
	if outcome.FallbackReason == autoclassifier.FallbackInvalidText {
		result.Code = "classifier_invalid_output"
		return result
	}
	result.Code = "classifier_unavailable"
	return result
}

func existingModelConfigured(settings contract.ClassifierSettings) bool {
	return settings.Enabled && settings.Source() == contract.ClassifierSourceExistingModel &&
		settings.ExistingModel != nil &&
		settings.ExistingModel.ServiceID != "" &&
		settings.ExistingModel.Model != ""
}

// classificationInstruction compiles the existing_model classification
// instruction from one immutable label snapshot. The output constraint's JSON
// shape demand and schema section is the whole instruction, so nothing stored
// can drop the shape requirement the strict parser depends on. There is
// deliberately no fallback to the legacy bare-label DefaultClassifierPrompt —
// an obedient answer to it can never parse.
func classificationInstruction(labels []string, dimensions []contract.ClassifierDimension) (string, error) {
	// The sub-call re-enters the pipeline as a native /v1/responses request;
	// with no verified native capability recorded the plan is JSON-only, and
	// SystemPrompt is identical in every constraint mode. With no dimensions
	// declared this is byte-identical to the single-dimension instruction.
	constraint, err := classifieroutput.PlanWithDimensions(contract.ProtocolOpenAIResponses, nil, labels, dimensions)
	if err != nil {
		return "", err
	}
	return constraint.SystemPrompt(), nil
}

// modelRoutingTaskTypeExistingModel classifies through an existing gateway
// model: one bounded in-process generation call, then the shared strict
// extraction core. Every failure maps to a fallback code; classification
// never blocks the request (fail-open is not negotiable).
func (handler *Handler) modelRoutingTaskTypeExistingModel(ctx context.Context, text string, group contract.ModelRoutingGroup, settings contract.ClassifierSettings) modelrouting.TaskTypeOutcome {
	plan := settings.ResolveCallPlan(&group)
	result := modelrouting.TaskTypeOutcome{
		ClassifierID:           "existing_model",
		LowConfidenceThreshold: plan.LowConfidenceThreshold,
	}
	instruction, err := classificationInstruction(plan.Labels, plan.Dimensions)
	if err != nil {
		// Unreachable with a validated group (the label snapshot is never
		// empty), but a broken catalog must still fail open, not block.
		result.Code = "classifier_unavailable"
		return result
	}
	start := time.Now()
	assistantText, err := handler.ClassifyModel(ctx, settings.ExistingModel.ServiceID, settings.ExistingModel.Model, instruction, text, plan.TimeoutMS)
	result.DurationMS = int(time.Since(start).Milliseconds())
	if err != nil {
		result.Code = existingModelFailureCode(ctx, err)
		return result
	}
	var parsed classifieroutput.TaskType
	if len(plan.Dimensions) == 0 {
		// Single dimension: the reply is the flat {label, score, candidates}
		// document it has always been, and this branch is the pre-dimensions
		// path verbatim.
		if parsed, err = classifieroutput.ParseTaskType(assistantText, plan.Labels); err != nil {
			if errors.Is(err, classifieroutput.ErrRefusal) {
				result.Code = "classifier_refusal"
				return result
			}
			result.Code = "classifier_invalid_output"
			return result
		}
	} else {
		// Multi dimension: one envelope keyed by dimension name, task_type
		// among them. A malformed dimension makes the reply unusable, exactly
		// as a malformed task_type would — the answer is one document, and
		// keeping the readable half would route on a partial reading.
		dimensions, dimensionErr := classifieroutput.ParseDimensions(assistantText, plan.Labels, plan.Dimensions)
		if dimensionErr != nil {
			if errors.Is(dimensionErr, classifieroutput.ErrRefusal) {
				result.Code = "classifier_refusal"
				return result
			}
			result.Code = "classifier_invalid_output"
			return result
		}
		taskType, ok := dimensions[contract.BuiltInTaskTypeDimension]
		if !ok {
			// Every custom dimension was answered but task_type was not. The
			// routing decision still needs a task_type, so this is the same
			// unusable reply as a malformed one.
			result.Code = "classifier_invalid_output"
			return result
		}
		result.Dimensions = dimensionScores(dimensions)
		// Carry the ranked sequence through: the evaluator's scored-fallback
		// substitution reads it, so dropping it here would disable substitution
		// for every group as soon as one custom dimension was configured.
		parsed = classifieroutput.TaskType{Label: taskType.Label, Score: taskType.Score, Candidates: taskType.Candidates}
	}
	if !group.ValidTaskTypeLabel(parsed.Label) {
		result.Code = "classifier_invalid_output"
		return result
	}
	if parsed.Score < plan.LowConfidenceThreshold {
		// Low confidence is not a failure and never takes a fallback: this
		// task_type stays unknown and threshold filtering empties its
		// candidates. The custom dimensions parsed from the same reply are
		// not affected — each is judged against the threshold on its own
		// score (modelrouting.DimensionFacts), and the recorded code below
		// tells that apart from a real fallback.
		result.Code = modelrouting.CodeClassifierLowConfidence
		result.Candidates = autoclassifierCandidates(parsed.Candidates)
		return result
	}
	result.Value = parsed.Label
	result.Confidence = parsed.Score
	result.Candidates = autoclassifierCandidates(parsed.Candidates)
	return result
}

// dimensionScores moves the strict extraction core's parsed dimensions onto
// the shared Outcome carrier, dropping the built-in task_type: it already
// travels in Value/Candidates and must never appear in the dimensions map,
// whose entries the fact builder turns into Facts.Dimensions.
func dimensionScores(dimensions map[string]classifieroutput.DimensionLabel) map[string]modelrouting.DimensionScore {
	if len(dimensions) == 0 {
		return nil
	}
	scores := make(map[string]modelrouting.DimensionScore, len(dimensions))
	for name, dimension := range dimensions {
		if name == contract.BuiltInTaskTypeDimension {
			continue
		}
		scores[name] = modelrouting.DimensionScore{Label: dimension.Label, Score: dimension.Score}
	}
	if len(scores) == 0 {
		return nil
	}
	return scores
}

// autoclassifierDimensionScores moves the cloud source's dimension answers onto
// the shared Outcome carrier. Both classifier sources (the strict extraction
// core and the cloud adapters) speak this one type downstream, so the fact
// builder sees a single shape regardless of where the answer came from.
func autoclassifierDimensionScores(dimensions map[string]autoclassifier.DimensionAnswer) map[string]modelrouting.DimensionScore {
	if len(dimensions) == 0 {
		return nil
	}
	scores := make(map[string]modelrouting.DimensionScore, len(dimensions))
	for name, dimension := range dimensions {
		if name == contract.BuiltInTaskTypeDimension {
			continue
		}
		scores[name] = modelrouting.DimensionScore{Label: dimension.Label, Score: dimension.Score}
	}
	if len(scores) == 0 {
		return nil
	}
	return scores
}

// autoclassifierCandidates moves the strict extraction core's ranked output
// onto the shared Outcome carrier so every classifier source speaks one
// candidate type downstream.
func autoclassifierCandidates(candidates []classifieroutput.Candidate) []autoclassifier.Candidate {
	if len(candidates) == 0 {
		return nil
	}
	converted := make([]autoclassifier.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		converted = append(converted, autoclassifier.Candidate{Label: candidate.Label, Score: candidate.Score})
	}
	return converted
}

func existingModelFailureCode(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, ErrClassificationVirtualModel):
		return "classifier_virtual_model_rejected"
	case errors.Is(err, ErrClassificationBusy):
		return "classifier_busy"
	case errors.Is(err, ErrClassificationTimeout), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "classifier_timeout"
	case errors.Is(err, ErrClassificationInvalidText):
		return "classifier_invalid_output"
	default:
		return "classifier_unavailable"
	}
}

// taskTypeCandidateScores / taskTypeCandidateLabels /
// taskTypeCandidateObservations moved to the modelrouting package
// (TaskTypeCandidateScores and friends) so the control-plane dry-run consumes
// the same conversion instead of a copy. Behavior is unchanged.

// modelRoutingTaskTypeCloud classifies through the configured external
// provider. Every failure mode returns a deterministic fallback: the request
// is never blocked by the classifier seam.
func (handler *Handler) modelRoutingTaskTypeCloud(ctx context.Context, text string, group contract.ModelRoutingGroup, settings contract.ClassifierSettings) modelrouting.TaskTypeOutcome {
	plan := settings.ResolveCallPlan(&group)
	result := modelrouting.TaskTypeOutcome{LowConfidenceThreshold: plan.LowConfidenceThreshold}
	if handler.proxyCredentials == nil {
		result.Code = autoclassifier.FallbackUnavailable
		return result
	}
	start := time.Now()
	secret, err := handler.proxyCredentials.Get(ctx, secretstore.Ref(plan.CredentialRef))
	if err != nil {
		result.DurationMS = int(time.Since(start).Milliseconds())
		result.Code = autoclassifier.FallbackUnavailable
		return result
	}
	outcome := cloud.Classify(ctx, nil, cloud.Input{
		Protocol:   cloud.Protocol(plan.Protocol),
		Endpoint:   plan.Endpoint,
		APIKey:     secret,
		Model:      plan.Model,
		Prompt:     plan.Prompt,
		Labels:     plan.Labels,
		Text:       text,
		TimeoutMS:  plan.TimeoutMS,
		Dimensions: plan.Dimensions,
	})
	result.DurationMS = int(time.Since(start).Milliseconds())
	result.ClassifierID = string(outcome.ClassifierID)
	result.Candidates = outcome.Candidates
	result.Dimensions = autoclassifierDimensionScores(outcome.Dimensions)
	if outcome.OK() {
		if !group.ValidTaskTypeLabel(outcome.Category) {
			result.Code = "classifier_invalid_output"
			return result
		}
		// A confident answer inside the label set is the only success path.
		// Low confidence is never a failure and never takes a fallback: the
		// fact becomes unknown and triUnknown rules skip. As on the
		// existing-model path, the dimensions measured by the same request
		// are unaffected — only this task_type's score was weak.
		if outcome.Confidence < plan.LowConfidenceThreshold {
			result.Code = modelrouting.CodeClassifierLowConfidence
			return result
		}
		result.Value = outcome.Category
		result.Confidence = outcome.Confidence
		return result
	}
	failed := classifierFailureTaskType(plan.Fallback, group, outcome, result.DurationMS)
	return modelrouting.TaskTypeOutcome{
		Value:                  failed.Value,
		Code:                   failed.Code,
		DurationMS:             failed.DurationMS,
		ClassifierID:           failed.ClassifierID,
		LowConfidenceThreshold: plan.LowConfidenceThreshold,
	}
}

// classifierFailureTaskType maps a failed cloud classification onto the
// configured fallback. fail-open is not negotiable: even fallback=error only
// records a code and returns the deterministic skip result.
func classifierFailureTaskType(fallback string, group contract.ModelRoutingGroup, outcome autoclassifier.Outcome, duration int) modelrouting.TaskTypeOutcome {
	var code string
	switch outcome.FallbackReason {
	case autoclassifier.FallbackBusy:
		code = "classifier_busy"
	case autoclassifier.FallbackTimeout:
		code = "classifier_timeout"
	case cloud.FallbackRefusal:
		code = "classifier_refusal"
	case cloud.FallbackInvalidOutput:
		code = "classifier_invalid_output"
	default:
		code = autoclassifier.FallbackUnavailable
	}
	identifier := string(outcome.ClassifierID)
	switch fallback {
	case contract.ClassifierFallbackError:
		return modelrouting.TaskTypeOutcome{Code: "classifier_error", DurationMS: duration, ClassifierID: identifier}
	case contract.ClassifierFallbackGeneral:
		if group.ValidTaskTypeLabel("general") {
			return modelrouting.TaskTypeOutcome{Value: "general", Code: code, DurationMS: duration, ClassifierID: identifier}
		}
		return modelrouting.TaskTypeOutcome{Code: "classifier_fallback_invalid", DurationMS: duration, ClassifierID: identifier}
	default:
		return modelrouting.TaskTypeOutcome{Code: code, DurationMS: duration, ClassifierID: identifier}
	}
}

func pinModelRoutingWebSocket(ctx context.Context, model string) {
	if turn := responsesWSTurnFromContext(ctx); turn != nil {
		turn.routingModel = model
	}
}

// modelRoutingNeedsClassification reports whether any rule reads a field the
// classifier produces. task_type is always a classifier field; each declared
// custom dimension is one too, so a group whose rules reference only a custom
// dimension still classifies. Without this the dimension would never be asked
// for, every rule testing it would stay unknown forever, and the configuration
// would silently do nothing.
//
// With no custom dimensions declared this reduces to the task_type-only gate
// the routing path has always used.
func modelRoutingNeedsClassification(group contract.ModelRoutingGroup, dimensions []contract.ClassifierDimension) bool {
	for _, rule := range group.Rules {
		if conditionNeedsField(rule.When, contract.BuiltInTaskTypeDimension) {
			return true
		}
		for _, dimension := range dimensions {
			if conditionNeedsField(rule.When, dimension.Name) {
				return true
			}
		}
	}
	return false
}

func conditionNeedsField(condition contract.ModelRoutingCondition, field string) bool {
	if condition.Field == field {
		return true
	}
	if condition.Not != nil && conditionNeedsField(*condition.Not, field) {
		return true
	}
	for _, child := range condition.All {
		if conditionNeedsField(child, field) {
			return true
		}
	}
	for _, child := range condition.Any {
		if conditionNeedsField(child, field) {
			return true
		}
	}
	return false
}

func routingBoolPtr(value bool) *bool { return &value }
func routingIntPtr(value int) *int    { return &value }
