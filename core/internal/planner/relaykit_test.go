package planner

import (
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// A model is converted into its format even though no capability row
// declares that protocol; any other upstream still needs a row.
func TestBuildRelayKitAcceptsTheModelFormatWithoutARow(t *testing.T) {
	service := contract.Service{
		ID: "service_newapi", Name: "New API", Kind: contract.ServiceKindNewAPI, Enabled: true,
		Models:         []string{"gemini-x"},
		ModelProtocols: []contract.ModelProtocol{{Model: "gemini-x", Protocol: contract.ProtocolGoogleGenerateContent}},
		Capabilities:   []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}},
		HTTP:           &contract.HTTPConnection{BaseURL: "https://newapi.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}},
	}
	input := RelayKitInput{
		Service: service, InputProtocol: contract.ProtocolOpenAIChat, UpstreamProtocol: contract.ProtocolGoogleGenerateContent,
		UpstreamModel: "gemini-x", Streaming: true,
		Edges: []contract.ConversionEdge{{From: contract.ProtocolOpenAIChat, To: contract.ProtocolGoogleGenerateContent, Quality: contract.ConversionQualityFair, Streaming: true}},
	}
	plan, err := BuildRelayKit(input)
	if err != nil || plan.Type != contract.PlanTypeRelayKit || plan.UpstreamProtocol != contract.ProtocolGoogleGenerateContent {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
	input.UpstreamModel = "gemini-y"
	var capability *CapabilityUnavailableError
	if _, err := BuildRelayKit(input); !errors.As(err, &capability) {
		t.Fatalf("unpinned model without a row: err = %v", err)
	}
}
