package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// heldFilter holds Inspect until released so a test can observe the
// record while the privacy decision is still outstanding.
type heldFilter struct {
	inner    privacy.Filter
	entered  chan struct{}
	release  chan struct{}
	err      error
	inspects int
}

func (filter *heldFilter) ResolvePolicy(ctx context.Context, scope privacy.Scope) (privacy.Policy, error) {
	return filter.inner.ResolvePolicy(ctx, scope)
}

func (filter *heldFilter) Inspect(
	ctx context.Context,
	policy privacy.Policy,
	protocol contract.ProtocolID,
	body []byte,
) (privacy.Result, error) {
	filter.inspects++
	if filter.entered != nil {
		close(filter.entered)
		<-filter.release
	}
	if filter.err != nil {
		return privacy.Result{}, filter.err
	}
	return filter.inner.Inspect(ctx, policy, protocol, body)
}

func exposureCaptureSettings() *memoryAuditSettings {
	return &memoryAuditSettings{settings: contract.AuditSettings{
		RequestBodyEnabled: true, ResponseContentEnabled: true,
		RequestBodyMaxBytes: 64 * 1024, ResponseContentMaxBytes: 64 * 1024,
		MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
}

// echoChatForwarder answers with the upstream message content, so a
// placeholder sent upstream comes back and is restored for the client.
func echoChatForwarder(t *testing.T) forwarderFunc {
	return func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &body); err != nil || len(body.Messages) == 0 {
			t.Fatalf("upstream body=%s err=%v", raw, err)
		}
		reply, err := json.Marshal(map[string]any{
			"id": "chatcmpl_exposure",
			"choices": []any{map[string]any{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": body.Messages[0].Content},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		if err != nil {
			return err
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, err = writer.Write(reply)
		return err
	}
}

func blobExposures(blobs *memoryAuditBlobs) map[storage.AuditDirection]storage.AuditExposure {
	result := make(map[storage.AuditDirection]storage.AuditExposure, len(blobs.blobs))
	for _, blob := range blobs.blobs {
		result[blob.Direction] = blob.Exposure
	}
	return result
}

func TestIngressAuditExposureFollowsPrivacyDecision(t *testing.T) {
	const withEmail = `{"model":"gpt-5","messages":[{"role":"user","content":"mail alice@example.com please"}]}`
	const plain = `{"model":"gpt-5","messages":[{"role":"user","content":"hello there"}]}`
	cases := []struct {
		name         string
		policy       *privacy.Policy
		body         string
		wantStatus   int
		wantDecision contract.PrivacyDecision
		want         map[storage.AuditDirection]storage.AuditExposure
		wantFindings []contract.PrivacyFinding
	}{
		{
			name: "no filter", body: withEmail, wantStatus: http.StatusOK,
			wantDecision: contract.PrivacyDecisionNone,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest:  storage.AuditExposureShareable,
				storage.AuditDirectionResponse: storage.AuditExposureShareable,
			},
		},
		{
			name:   "disabled policy",
			policy: &privacy.Policy{Enabled: false}, body: withEmail, wantStatus: http.StatusOK,
			wantDecision: contract.PrivacyDecisionNone,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest:  storage.AuditExposureShareable,
				storage.AuditDirectionResponse: storage.AuditExposureShareable,
			},
		},
		{
			name:   "allow",
			policy: &privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true},
			body:   plain, wantStatus: http.StatusOK,
			wantDecision: contract.PrivacyDecisionAllow,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest:  storage.AuditExposureShareable,
				storage.AuditDirectionResponse: storage.AuditExposureShareable,
			},
		},
		{
			name:   "warn",
			policy: &privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionWarn},
			body:   withEmail, wantStatus: http.StatusOK,
			wantDecision: contract.PrivacyDecisionWarn,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest:  storage.AuditExposureShareable,
				storage.AuditDirectionResponse: storage.AuditExposureShareable,
			},
		},
		{
			name:   "redact with restore",
			policy: &privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true},
			body:   withEmail, wantStatus: http.StatusOK,
			wantDecision: contract.PrivacyDecisionRedact,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest:         storage.AuditExposureRaw,
				storage.AuditDirectionResponse:        storage.AuditExposureRaw,
				storage.AuditDirectionUpstreamRequest: storage.AuditExposureShareable,
			},
			wantFindings: []contract.PrivacyFinding{{Kind: contract.CanonicalKindEmail, JSONPath: "/messages/0/content", Count: 1}},
		},
		{
			name:   "redact without restore",
			policy: &privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact},
			body:   withEmail, wantStatus: http.StatusOK,
			wantDecision: contract.PrivacyDecisionRedact,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest:  storage.AuditExposureRaw,
				storage.AuditDirectionResponse: storage.AuditExposureShareable,
			},
			wantFindings: []contract.PrivacyFinding{{Kind: contract.CanonicalKindEmail, JSONPath: "/messages/0/content", Count: 1}},
		},
		{
			name:   "block",
			policy: &privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock},
			body:   withEmail, wantStatus: http.StatusForbidden,
			wantDecision: contract.PrivacyDecisionBlock,
			want: map[storage.AuditDirection]storage.AuditExposure{
				storage.AuditDirectionRequest: storage.AuditExposureRaw,
			},
			wantFindings: []contract.PrivacyFinding{{Kind: contract.CanonicalKindEmail, JSONPath: "/messages/0/content", Count: 1}},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			records := &memoryRequestRecordStore{}
			blobs := &memoryAuditBlobs{records: records}
			var filter privacy.Filter
			if testCase.policy != nil {
				filter = testPrivacyEngine(t, *testCase.policy, nil)
			}
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: []endpoint.Resolved{{
					Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false),
				}}},
				PrivacyFilter:  filter,
				RequestRecords: records,
				AuditSettings:  exposureCaptureSettings(),
				AuditBlobs:     blobs,
				Forwarder:      echoChatForwarder(t),
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			got := blobExposures(blobs)
			for direction, want := range testCase.want {
				if got[direction] != want {
					t.Fatalf("%s exposure=%q want %q (all %v)", direction, got[direction], want, got)
				}
			}
			for direction, exposure := range got {
				if _, listed := testCase.want[direction]; !listed && exposure != storage.AuditExposureShareable {
					t.Fatalf("%s exposure=%q, want shareable", direction, exposure)
				}
			}
			if len(records.records) != 1 {
				t.Fatalf("records=%#v", records.records)
			}
			record := records.records[0]
			if record.PrivacyDecision == nil || *record.PrivacyDecision != testCase.wantDecision {
				t.Fatalf("decision=%v want %s", record.PrivacyDecision, testCase.wantDecision)
			}
			if len(record.PrivacyFindings) != len(testCase.wantFindings) {
				t.Fatalf("findings=%#v want %#v", record.PrivacyFindings, testCase.wantFindings)
			}
			for index := range testCase.wantFindings {
				if record.PrivacyFindings[index] != testCase.wantFindings[index] {
					t.Fatalf("findings=%#v want %#v", record.PrivacyFindings, testCase.wantFindings)
				}
			}
			encoded, _ := json.Marshal(record.PrivacyFindings)
			if strings.Contains(string(encoded), "alice@example.com") {
				t.Fatalf("findings carry a request value: %s", encoded)
			}
			if err := record.Validate(); err != nil {
				t.Fatalf("record invalid: %v", err)
			}
		})
	}
}

func TestIngressAuditExposureSettlesPendingRequestAfterDecision(t *testing.T) {
	const body = `{"model":"gpt-5","messages":[{"role":"user","content":"hello there"}]}`
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{records: records}
	filter := &heldFilter{
		inner: testPrivacyEngine(t, privacy.Policy{
			Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact,
		}, nil),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false),
		}}},
		PrivacyFilter:  filter,
		RequestRecords: records,
		AuditSettings:  exposureCaptureSettings(),
		AuditBlobs:     blobs,
		Forwarder:      echoChatForwarder(t),
	})
	done := make(chan int)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		done <- response.Code
	}()
	<-filter.entered
	if got := blobExposures(blobs)[storage.AuditDirectionRequest]; got != storage.AuditExposurePending {
		t.Fatalf("undecided request exposure=%q, want pending", got)
	}
	updatesBeforeDecision := blobs.exposureUpdates
	close(filter.release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if got := blobExposures(blobs)[storage.AuditDirectionRequest]; got != storage.AuditExposureShareable {
		t.Fatalf("settled request exposure=%q, want shareable", got)
	}
	// One relabel, written with the metadata that follows the decision.
	if blobs.exposureUpdates-updatesBeforeDecision != 1 {
		t.Fatalf("exposure updates=%d, want 1", blobs.exposureUpdates-updatesBeforeDecision)
	}
	if filter.inspects != 1 {
		t.Fatalf("inspections=%d, want 1", filter.inspects)
	}
}

func TestIngressAuditExposureWithholdsBodyWhenInspectionFails(t *testing.T) {
	const body = `{"model":"gpt-5","messages":[{"role":"user","content":"hello there"}]}`
	records := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{records: records}
	filter := &heldFilter{
		inner: testPrivacyEngine(t, privacy.Policy{
			Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact,
		}, nil),
		err: privacy.ErrPolicyUnavailable,
	}
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false),
		}}},
		PrivacyFilter:  filter,
		RequestRecords: records,
		AuditSettings:  exposureCaptureSettings(),
		AuditBlobs:     blobs,
		Forwarder: forwarderFunc(func(http.ResponseWriter, *http.Request, transport.Target) error {
			return errors.New("must not forward")
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if got := blobExposures(blobs)[storage.AuditDirectionRequest]; got != storage.AuditExposureRaw {
		t.Fatalf("undecided request exposure=%q, want raw", got)
	}
	if len(records.records) != 1 || records.records[0].PrivacyDecision != nil {
		t.Fatalf("records=%#v, want no decision", records.records)
	}
}

func TestPrivacyFindingPathsKeepStructureOnly(t *testing.T) {
	cases := map[string]string{
		"/messages/0/content":                  "/messages/0/content",
		"/input/2/arguments/alice@example.com": "/input/2/arguments/*",
		"/input/0/arguments/to~1cc":            "/input/0/arguments/*",
		"":                                     "/",
		"/":                                    "/",
		"/contents/0/parts/1/text":             "/contents/0/parts/1/text",
		"/messages/0/content/0/input/Customer ID": "/messages/0/content/0/input/*",
	}
	for pointer, want := range cases {
		if got := privacyFindingPath(pointer); got != want {
			t.Fatalf("privacyFindingPath(%q)=%q want %q", pointer, got, want)
		}
	}
	long := privacyFindingPath("/" + strings.Repeat("a/", 200))
	if len([]rune(long)) > contract.MaxPrivacyFindingPathRunes {
		t.Fatalf("path runes=%d", len([]rune(long)))
	}

	findings := []privacy.Finding{
		{Kind: privacy.KindEmail, Path: "/messages/0/content"},
		{Kind: privacy.KindEmail, Path: "/messages/0/content"},
		{Kind: privacy.Kind("not_a_kind"), Path: "/messages/0/content"},
		{Kind: privacy.KindEmail, Path: "/messages/1/content"},
	}
	merged := mergePrivacyFindings(nil, findings)
	if len(merged) != 2 || merged[0].Count != 2 || merged[1].Count != 1 {
		t.Fatalf("merged=%#v", merged)
	}
	many := make([]privacy.Finding, 0, contract.MaxPrivacyFindings+10)
	for index := 0; index < cap(many); index++ {
		many = append(many, privacy.Finding{Kind: privacy.KindEmail, Path: "/messages/" + strconv.Itoa(index) + "/content"})
	}
	if capped := mergePrivacyFindings(nil, many); len(capped) != contract.MaxPrivacyFindings {
		t.Fatalf("findings=%d, want cap %d", len(capped), contract.MaxPrivacyFindings)
	}
}
