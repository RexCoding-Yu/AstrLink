package ingress

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
)

// beginRequestPlugins starts the request processing instances of a request
// and runs their ingress hooks over the client's body, as the privacy check
// leaves it for a plugin the user does not trust. A changed body replaces
// the request's, which is classified again; the model and stream stay. It
// reports true when an instance stopped the request, which then has its
// answer.
func (handler *Handler) beginRequestPlugins(writer http.ResponseWriter, request *http.Request, classified Request, session *recordSession) (*http.Request, Request, bool) {
	if handler.plugins == nil || session == nil || len(classified.body) == 0 {
		return request, classified, false
	}
	run := handler.plugins.RequestPlugins(classified.Model, request.URL.Path)
	if run == nil {
		return request, classified, false
	}
	session.requestPlugins = run
	accessTokenID, _ := AccessTokenIDFromContext(request.Context())
	session.requestPluginView = handler.pluginBodyView(session, classified, accessTokenID)
	body, refusal := run.Ingress(request.Context(), classified.Protocol, classified.body, session.requestPluginView)
	if refusal != nil {
		session.captureUnreadRequestBody(request)
		writeInferenceError(writer, refusal.Status, refusal.Code, refusal.Message, false, nil)
		session.noteFailed(errorSummaryFromInference(refusal.Code, refusal.Message, false))
		return request, classified, true
	}
	if bytes.Equal(body, classified.body) {
		return request, classified, false
	}
	previous := request.Body
	request.Body = &replayReadCloser{reader: bytes.NewReader(body), closer: previous}
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	reclassified, err := classify(request, handler.maxRequestBodyBytes)
	if err != nil {
		writeInferenceError(writer, http.StatusInternalServerError, contract.RequestPluginFailedCode, "A request plugin failed", false, nil)
		session.noteFailed(errorSummaryFromInference(contract.RequestPluginFailedCode, "A request plugin failed", false))
		return request, classified, true
	}
	reclassified.sentBodyHeaders = classified.sentBodyHeaders
	session.classified = reclassified
	return request, reclassified, false
}

// outboundRequestPlugins runs the outbound and egress hooks over the body
// one attempt sends, in its provider's protocol, and adds the headers they
// set. It returns the refusal of an instance that stopped the request.
func (session *recordSession) outboundRequestPlugins(ctx context.Context, attempt *http.Request, candidate endpoint.Resolved, protocol contract.ProtocolID, model string, index int) *pluginhost.RequestRefusal {
	run := session.requestPluginRun()
	if run == nil || (!run.Serves(contract.RequestStageOutbound) && !run.Serves(contract.RequestStageEgress)) {
		return nil
	}
	data, err := io.ReadAll(attempt.Body)
	if err != nil {
		return &pluginhost.RequestRefusal{Status: http.StatusInternalServerError, Code: contract.RequestPluginFailedCode, Message: "A request plugin failed"}
	}
	edited, refusal := run.Outbound(ctx, pluginhost.RequestAttempt{Protocol: protocol, UpstreamModel: model, Index: index}, data, attempt.Header, session.requestPluginView)
	replaceRecoveryRequestBody(attempt, edited)
	return refusal
}

// requestPluginRun is the request's request processing, nil without one.
func (session *recordSession) requestPluginRun() *pluginhost.RequestRun {
	if session == nil {
		return nil
	}
	return session.requestPlugins
}

// reportRequestPlugins hands the finished request to the report hooks of
// its request processing instances.
func (session *recordSession) reportRequestPlugins() {
	run := session.requestPluginRun()
	if !run.ServesReport() {
		return
	}
	record := pluginhost.RequestReport{
		RequestID: string(session.id), StartedAt: session.startedAt.UnixMilli(),
		DurationMs: time.Since(session.startedAt).Milliseconds(),
		Request:    pluginhost.ReportRequest{Body: session.classified.body},
		Format:     session.classified.Protocol,
	}
	if scanner := session.upstreamScanner; scanner != nil && !scanner.firstOutputAt.IsZero() {
		first := max(0, scanner.firstOutputAt.Sub(session.startedAt).Milliseconds())
		record.FirstResponseMs = &first
	}
	if session.hasHTTPStatus {
		record.Response = &pluginhost.ReportResponse{Status: session.httpStatus, Stream: session.classified.Streaming}
	}
	if usage := session.scanner.Usage(); usage != nil {
		record.Usage = map[string]int{"promptTokens": usage.InputTokens, "completionTokens": usage.OutputTokens}
		if usage.CacheReadTokens != nil {
			record.Usage["cacheTokens"] = *usage.CacheReadTokens
		}
	}
	if summary := session.errorSummary; summary != nil {
		record.Error = &pluginhost.ReportError{Status: session.httpStatus, Code: summary.Code, Type: summary.Category, Message: summary.Message}
	}
	run.Report(record)
}
