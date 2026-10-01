package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

// The projections below are what agents see of services and policies.
// They are allowlists: a field reaches the agent only if it is copied here, so
// credential references, proxy addresses, provider account ids, and literal
// allowlist or regex values never leave the Control API through the CLI.

const (
	maxServicePages           = 10
	serviceStatusRequestLimit = 20
	serviceStatusRiskLimit    = 5
)

type serviceView struct {
	ID           contract.ServiceID     `json:"id"`
	Name         string                 `json:"name"`
	Kind         contract.ServiceKind   `json:"kind"`
	Enabled      bool                   `json:"enabled"`
	Models       []string               `json:"models"`
	Capabilities []contract.Capability  `json:"capabilities"`
	BaseOrigin   string                 `json:"base_origin,omitempty"`
	Subscription *subscriptionStateView `json:"subscription,omitempty"`
}

type subscriptionStateView struct {
	Provider       contract.SubscriptionProvider `json:"provider"`
	Status         contract.SubscriptionStatus   `json:"status"`
	AccountHint    string                        `json:"account_hint,omitempty"`
	TokenExpiresAt *time.Time                    `json:"token_expires_at,omitempty"`
	LastRefreshAt  *time.Time                    `json:"last_refresh_at,omitempty"`
	LastError      *contract.SubscriptionError   `json:"last_error,omitempty"`
	Risk           *contract.SubscriptionRisk    `json:"risk,omitempty"`
}

func projectService(service contract.Service) serviceView {
	view := serviceView{
		ID:           service.ID,
		Name:         service.Name,
		Kind:         service.Kind,
		Enabled:      service.Enabled,
		Models:       service.Models,
		Capabilities: service.Capabilities,
	}
	if view.Models == nil {
		view.Models = []string{}
	}
	if view.Capabilities == nil {
		view.Capabilities = []contract.Capability{}
	}
	if service.HTTP != nil {
		view.BaseOrigin = urlOrigin(service.HTTP.BaseURL)
	}
	if connection := service.Subscription; connection != nil {
		view.Subscription = &subscriptionStateView{
			Provider:       connection.Provider,
			Status:         connection.Status,
			AccountHint:    connection.AccountHint,
			TokenExpiresAt: connection.TokenExpiresAt,
			LastRefreshAt:  connection.LastRefreshAt,
			LastError:      connection.LastError,
			Risk:           connection.Risk,
		}
	}
	return view
}

// urlOrigin keeps only scheme and host. Paths are dropped as well as userinfo
// and query because some gateways put a key in the path.
func urlOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

func listServices(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
	query := url.Values{"limit": {"200"}}
	if value, ok := arguments["enabled"].(bool); ok {
		query.Set("enabled", fmt.Sprint(value))
	}
	items := []serviceView{}
	for page := 0; page < maxServicePages; page++ {
		raw, err := client.get(ctx, controlapi.ServicesPath, query)
		if err != nil {
			return nil, err
		}
		var decoded struct {
			Items      []contract.Service `json:"items"`
			NextCursor *string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("decode services: %w", err)
		}
		for _, service := range decoded.Items {
			items = append(items, projectService(service))
		}
		if decoded.NextCursor == nil || *decoded.NextCursor == "" {
			break
		}
		query.Set("cursor", *decoded.NextCursor)
	}
	return json.Marshal(map[string]any{"items": items})
}

type recentRequestsView struct {
	Sampled       int                            `json:"sampled"`
	ByStatus      map[contract.RequestStatus]int `json:"by_status"`
	LastSuccessAt *time.Time                     `json:"last_success_at,omitempty"`
	LastFailure   *recentFailureView             `json:"last_failure,omitempty"`
}

type recentFailureView struct {
	RequestID  contract.RequestID     `json:"request_id"`
	StartedAt  time.Time              `json:"started_at"`
	Status     contract.RequestStatus `json:"status"`
	HTTPStatus *int                   `json:"http_status,omitempty"`
	Error      *contract.ErrorSummary `json:"error,omitempty"`
}

func summarizeRecentRequests(records []contract.RequestRecord) recentRequestsView {
	view := recentRequestsView{Sampled: len(records), ByStatus: map[contract.RequestStatus]int{}}
	// Records arrive newest first, so the first match of each kind is the latest.
	for _, record := range records {
		status := record.EffectiveStatus()
		view.ByStatus[status]++
		switch status {
		case contract.RequestStatusSucceeded:
			if view.LastSuccessAt == nil {
				started := record.StartedAt
				view.LastSuccessAt = &started
			}
		case contract.RequestStatusFailed, contract.RequestStatusBlocked:
			if view.LastFailure == nil {
				view.LastFailure = &recentFailureView{
					RequestID:  record.ID,
					StartedAt:  record.StartedAt,
					Status:     status,
					HTTPStatus: record.HTTPStatus,
					Error:      record.Error,
				}
			}
		}
	}
	return view
}

func getServiceStatus(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
	id, err := requiredID(arguments)
	if err != nil {
		return nil, err
	}
	raw, err := client.get(ctx, controlapi.ServicesPath+"/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var service contract.Service
	if err := json.Unmarshal(raw, &service); err != nil {
		return nil, fmt.Errorf("decode service: %w", err)
	}
	result := map[string]any{"service": projectService(service)}

	raw, err = client.get(ctx, controlapi.RequestsPath, url.Values{
		"service_id": {id},
		"limit":      {fmt.Sprint(serviceStatusRequestLimit)},
	})
	if err != nil {
		return nil, err
	}
	var records struct {
		Items []contract.RequestRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode request records: %w", err)
	}
	result["recent_requests"] = summarizeRecentRequests(records.Items)

	if service.Kind.IsSubscription() {
		raw, err = client.get(ctx, controlapi.ServicesPath+"/"+url.PathEscape(id)+"/risk-events", url.Values{
			"limit": {fmt.Sprint(serviceStatusRiskLimit)},
		})
		var apiErr *APIError
		switch {
		case err == nil:
			var events struct {
				Items []contract.SubscriptionRiskEvent `json:"items"`
			}
			if err := json.Unmarshal(raw, &events); err != nil {
				return nil, fmt.Errorf("decode risk events: %w", err)
			}
			result["risk_events"] = events.Items
		case errors.As(err, &apiErr) && (apiErr.Status == http.StatusServiceUnavailable || apiErr.Status == http.StatusConflict):
			// Subscription runtime not loaded; the service document is still useful.
		default:
			return nil, err
		}
	}
	return json.Marshal(result)
}

func getPrivacyPolicy(ctx context.Context, client *Client, _ map[string]any) (json.RawMessage, error) {
	raw, err := client.get(ctx, controlapi.PoliciesPath, nil)
	if err != nil {
		return nil, err
	}
	// Core summarizes policies for the observer token the CLI holds. A
	// full policy does not decode into the summary, so it never passes through.
	var decoded struct {
		Items []controlapi.PolicySummary `json:"items"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode policies: %w", err)
	}
	return json.Marshal(map[string]any{
		"items": decoded.Items,
		"note":  "Allowlist entries and custom regex patterns are reported as counts and types only.",
	})
}

func explainRequest(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
	id, err := requiredID(arguments)
	if err != nil {
		return nil, err
	}
	base := controlapi.RequestsPath + "/" + url.PathEscape(id)
	raw, err := client.get(ctx, base, nil)
	if err != nil {
		return nil, err
	}
	var record contract.RequestRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode request record: %w", err)
	}
	children := json.RawMessage(`{"items":[]}`)
	if record.ChildCount > 0 {
		if children, err = client.get(ctx, base+"/children", nil); err != nil {
			return nil, err
		}
	}
	summary := map[string]any{
		"status":   record.EffectiveStatus(),
		"attempts": 1 + record.ChildCount,
	}
	if record.ServiceID != nil {
		summary["service_id"] = *record.ServiceID
	}
	if record.HTTPStatus != nil {
		summary["http_status"] = *record.HTTPStatus
	}
	if record.Error != nil {
		summary["error"] = record.Error
	}
	if record.RoutingDecision != nil && record.RoutingDecision.Selected != "" {
		summary["routing_selected"] = record.RoutingDecision.Selected
	}
	audit := record.Audit
	bodiesCaptured := audit.RequestBodyCaptured || audit.ResponseContentCaptured ||
		audit.UpstreamRequestBodyCaptured || audit.UpstreamResponseContentCaptured
	auditView := map[string]any{
		"bodies_captured": bodiesCaptured,
		"parts":           audit,
	}
	if bodiesCaptured {
		auditView["hint"] = "Run `astrlink audit <id>` for the captured content."
	}
	return json.Marshal(map[string]any{
		"summary":  summary,
		"record":   json.RawMessage(raw),
		"children": children,
		"audit":    auditView,
	})
}
