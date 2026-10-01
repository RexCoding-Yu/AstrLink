package endpoint

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// rankingStore honors the enabled filter like SQLite.
type rankingStore struct {
	services []contract.Service
	order    []contract.ServiceID
}

func (store rankingStore) ListServices(_ context.Context, options storage.ServiceListOptions) (storage.ServicePage, error) {
	items := make([]storage.ServiceRecord, 0, len(store.services))
	for _, service := range store.services {
		if options.Enabled != nil && service.Enabled != *options.Enabled {
			continue
		}
		items = append(items, storage.ServiceRecord{Service: service})
	}
	return storage.ServicePage{Items: items}, nil
}

func (store rankingStore) GetServiceOrder(context.Context) (storage.ServiceOrderRecord, error) {
	return storage.ServiceOrderRecord{Order: contract.ServiceOrder{ServiceIDs: store.order}}, nil
}

func (rankingStore) UpdateServiceOrder(context.Context, contract.ServiceOrder, string) (storage.ServiceOrderRecord, error) {
	return storage.ServiceOrderRecord{}, errors.New("read only")
}

func TestStoreResolverRanksEveryServiceWithItsSkipReason(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	http := func(id contract.ServiceID, models []string, capabilities ...contract.Capability) contract.Service {
		endpoint := resolverEndpoint(id, contract.CapabilityModeNative, true, models)
		if len(capabilities) > 0 {
			endpoint.Capabilities = capabilities
		}
		return contract.ServiceFromEndpoint(endpoint)
	}
	disabled := http("svc_disabled", nil)
	disabled.Enabled = false
	disabledUnlisted := http("svc_disabled_unlisted", []string{"other"})
	disabledUnlisted.Enabled = false
	kind := contract.ServiceKindCodexSubscription
	paused := contract.Service{
		ID: "svc_paused", Name: "paused", Kind: kind, Enabled: true, Models: []string{"gpt-5"},
		Capabilities: kind.SubscriptionProvider().Capabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected,
			CredentialRef: "keyring://subscription/svc_paused",
			Risk: &contract.SubscriptionRisk{
				State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeOrganizationDisabled, ObservedAt: now,
			},
		},
	}
	services := []contract.Service{
		// Listed out of priority order on purpose.
		http("svc_second", nil),
		disabled,
		disabledUnlisted,
		http("svc_unlisted", []string{"other"}),
		http("svc_chat_only", nil, contract.Capability{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}),
		http("svc_no_stream", nil, contract.Capability{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative}),
		http("svc_limited", nil),
		paused,
		http("svc_first", nil),
	}
	order := []contract.ServiceID{
		// svc_deleted stays in the order after its service is gone.
		"svc_disabled", "svc_disabled_unlisted", "svc_deleted", "svc_unlisted", "svc_chat_only", "svc_no_stream", "svc_limited",
		"svc_paused", "svc_first", "svc_second",
	}
	resolver, err := NewStoreResolver(rankingStore{services: services, order: order})
	if err != nil {
		t.Fatal(err)
	}
	resolver.clock = func() time.Time { return now }
	resolver.RecordRateLimit(Resolved{
		Service: http("svc_limited", nil), Mode: contract.CapabilityModeNative,
		UpstreamProtocol: contract.ProtocolOpenAIResponses, UpstreamModel: "gpt-5",
	}, time.Minute)

	request := ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, AllCandidates: true}
	candidates, ranking, err := resolver.ResolveRankedCandidates(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Service.ID != "svc_first" || candidates[1].Service.ID != "svc_second" {
		t.Fatalf("candidates = %#v", candidates)
	}
	want := []RankedService{
		{ServiceID: "svc_disabled", Skip: contract.RoutingSkipDisabled},
		// Not listing the model matters more than being disabled.
		{ServiceID: "svc_disabled_unlisted", Skip: contract.RoutingSkipModelNotListed},
		{ServiceID: "svc_unlisted", Skip: contract.RoutingSkipModelNotListed},
		{ServiceID: "svc_chat_only", Skip: contract.RoutingSkipProtocolUnsupported},
		{ServiceID: "svc_no_stream", Skip: contract.RoutingSkipStreamingUnsupported},
		{ServiceID: "svc_limited", Skip: contract.RoutingSkipRateLimited},
		{ServiceID: "svc_paused", Skip: contract.RoutingSkipRiskPaused},
		{ServiceID: "svc_first"},
		{ServiceID: "svc_second"},
	}
	if !reflect.DeepEqual(ranking, want) {
		t.Fatalf("ranking = %#v\nwant %#v", ranking, want)
	}
	plain, err := resolver.ResolveCandidates(context.Background(), request)
	if err != nil || !reflect.DeepEqual(plain, candidates) {
		t.Fatalf("ResolveCandidates = %#v, %v", plain, err)
	}

	// Failures still explain every provider, and none lists the model
	// whatever its state.
	_, ranking, err = resolver.ResolveRankedCandidates(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses, Model: "missing", Streaming: true,
	})
	var unavailable *CapabilityUnavailableError
	if !errors.As(err, &unavailable) || len(ranking) != len(want) {
		t.Fatalf("missing model ranking = %#v, err = %v", ranking, err)
	}
	for _, ranked := range ranking {
		if ranked.Skip != contract.RoutingSkipModelNotListed {
			t.Fatalf("missing model ranked %s as %q", ranked.ServiceID, ranked.Skip)
		}
	}
	for _, id := range []contract.ServiceID{"svc_first", "svc_second"} {
		resolver.RecordRateLimit(Resolved{
			Service: http(id, nil), Mode: contract.CapabilityModeNative,
			UpstreamProtocol: contract.ProtocolOpenAIResponses, UpstreamModel: "gpt-5",
		}, time.Minute)
	}
	_, ranking, err = resolver.ResolveRankedCandidates(context.Background(), request)
	var limited *RateLimitedCandidatesError
	if !errors.As(err, &limited) || len(limited.Limits) != 3 ||
		limited.Limits[1].Service != "svc_first" || limited.Limits[1].Model != "gpt-5" ||
		!limited.RetryAt().Equal(now.Add(time.Minute)) {
		t.Fatalf("all rate limited err = %#v", err)
	}
	want[7].Skip, want[8].Skip = contract.RoutingSkipRateLimited, contract.RoutingSkipRateLimited
	if !reflect.DeepEqual(ranking, want) {
		t.Fatalf("all rate limited ranking = %#v", ranking)
	}
}

func TestPlanServiceReportsTheFurthestFailedCheck(t *testing.T) {
	service := contract.Service{
		ID: "service_opencode", Kind: contract.ServiceKindOpenCodeGo, Enabled: true,
		Models:       []string{"kimi-k3"},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	request := ResolveRequest{Protocol: contract.ProtocolAnthropicMessages, Model: "kimi-k3", Streaming: true}
	runtime := contract.RuntimeProfile{RelayKitAvailable: true, Edges: []contract.ConversionEdge{{From: request.Protocol, To: contract.ProtocolOpenAIChat, Streaming: true}}}
	if _, skip := planService(service, request, "", runtime); skip != contract.RoutingSkipProtocolUnsupported {
		t.Fatalf("missing native protocol skip = %q", skip)
	}
	service.Capabilities = append(service.Capabilities, contract.Capability{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative})
	if _, skip := planService(service, request, "", runtime); skip != contract.RoutingSkipStreamingUnsupported {
		t.Fatalf("non-streaming native protocol skip = %q", skip)
	}
	service.Capabilities[1].Streaming = true
	if _, skip := planService(service, request, "", contract.RuntimeProfile{}); skip != contract.RoutingSkipConversionUnavailable {
		t.Fatalf("no converter skip = %q", skip)
	}
	if resolved, skip := planService(service, request, "", runtime); skip != "" || resolved.PlanType != contract.PlanTypeRelayKit {
		t.Fatalf("converted plan = %#v, %q", resolved, skip)
	}
}
