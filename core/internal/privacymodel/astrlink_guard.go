package privacymodel

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	astrLinkGuardRepoID   = "QuantumNous/astrlink-guard"
	astrLinkGuardRevision = "49e8b7b83d34fd75a86cf07698bd80972d444ff3"
	astrLinkGuardVersion  = "0.1.0"
	astrLinkGuardLicense  = "LICENSE"

	// The Hub counts a download only for requests to the repository's root
	// config.json, which the variant folders never match. This is the first
	// commit that has one.
	astrLinkGuardCountRevision = "5fb09dd8831224273c8db11bb0ed4b0890bd42e9"
	astrLinkGuardCountTimeout  = 10 * time.Second

	astrLinkGuardDecoderContractField = "astrlink_guard_decoder_contract"
	astrLinkGuardDecoderContract      = "bio-offset-consistency-v1"
	astrLinkGuardSpanContractField    = "astrlink_guard_span_contract"
	astrLinkGuardSpanContract         = "existing-ip-and-dynamic-url-v1"
)

// Guard labels in id2label order; each label is its own canonical kind.
var astrLinkGuardKinds = [...]contract.CanonicalKind{
	contract.CanonicalKindEmail, contract.CanonicalKindPhone,
	contract.CanonicalKindAccount, contract.CanonicalKindPaymentCard,
	contract.CanonicalKindIPAddress, contract.CanonicalKindURL,
	contract.CanonicalKindCommonSecret, contract.CanonicalKindAddress,
	contract.CanonicalKindDate, contract.CanonicalKindPerson,
}

func astrLinkGuardCatalogEntry() contract.PrivacyModelCatalogItem {
	version := astrLinkGuardVersion
	return contract.PrivacyModelCatalogItem{
		ID: CatalogAstrLinkGuard, Name: "AstrLink Guard",
		Summary:  "AstrLink's own compact Chinese and English privacy model, tuned for prompts and code. Detects personal information, credentials, IP addresses and URLs locally.",
		Source:   contract.PrivacyModelCatalogSourceOfficial,
		RepoID:   astrLinkGuardRepoID,
		Revision: astrLinkGuardRevision, Version: &version,
		License: "Apache-2.0", Languages: []string{"zh", "en"},
		Adapter: contract.PrivacyModelAdapterHFToken, Recommended: true,
		Variants: []contract.PrivacyModelVariant{
			{
				ID: "cpu_int8", Name: "CPU INT8", Quantization: "int8",
				BytesTotal: 38_886_573, EstimatedRAMBytes: 268_435_456,
				Recommended: true, Supported: true,
			},
			{
				ID: "cpu_fp32", Name: "CPU FP32", Quantization: "fp32",
				BytesTotal: 149_179_699, EstimatedRAMBytes: 402_653_184,
				Supported: true,
			},
		},
	}
}

// Each variant is a self-contained folder; releases keep this layout and
// change only file contents.
func astrLinkGuardLayout(variant string) (string, string, bool) {
	switch variant {
	case "cpu_int8":
		return "int8", "model_int8.onnx", true
	case "cpu_fp32":
		return "fp32", "model.onnx", true
	default:
		return "", "", false
	}
}

func astrLinkGuardFolderFiles(model string) []string {
	return []string{
		model, model + "_data",
		"config.json", "tokenizer.json", "tokenizer_config.json",
	}
}

func astrLinkGuardRuntime(variant string) runtimeSpec {
	folder, model, _ := astrLinkGuardLayout(variant)
	runtime := hfRuntime(folder + "/" + model)
	runtime.externalData = []string{folder + "/" + model + "_data"}
	runtime.tokenizerPath = folder + "/tokenizer.json"
	runtime.configPath = folder + "/config.json"
	tokenTypeIDs := "token_type_ids"
	runtime.inputNames.TokenTypeIDs = &tokenTypeIDs
	return runtime
}

func astrLinkGuardAssets(variant string) []Asset {
	shared := []Asset{
		{Path: "config.json", Size: 1_804, SHA256: "2c20003590d642e273c03c4c7c82d9849fa75f64bf29a71ec0a0c9a20ba4a150"},
		{Path: "tokenizer.json", Size: 1_502_881, SHA256: "0cf3b235ef125658015a6c8026a5fe6f28088bf825101a990ec18a10200feef4"},
		{Path: "tokenizer_config.json", Size: 205, SHA256: "3a7cf4d70bb7c0376f18cc6ad23c143853de5b4478084c414d5dbbd35ea5c589"},
	}
	var assets []Asset
	switch variant {
	case "cpu_int8":
		assets = []Asset{
			{Path: "model_int8.onnx", Size: 324_054, SHA256: "6cacc676cf1eea9fccc9654c0587484c181102b5835600ffaf1362e53c2641a9"},
			{Path: "model_int8.onnx_data", Size: 37_046_272, SHA256: "fe5918effd2a04684d071aa4e6bf880963318c4ed6edd6189a1ae439f225c83b"},
		}
	case "cpu_fp32":
		assets = []Asset{
			{Path: "model.onnx", Size: 219_740, SHA256: "f94aa70c09f9a3f424701df9d1bca366d631504d24a97c6c52346d7ce60f980f"},
			{Path: "model.onnx_data", Size: 147_443_712, SHA256: "da97f2b4a9fc59a373e1932ed6d6849dfe17e13260216463c7e60d917bf05806"},
		}
	default:
		return nil
	}
	folder, _, _ := astrLinkGuardLayout(variant)
	result := make([]Asset, 0, len(assets)+len(shared)+1)
	for _, asset := range append(assets, shared...) {
		asset.Path = folder + "/" + asset.Path
		result = append(result, asset)
	}
	return append(result, Asset{
		Path: astrLinkGuardLicense, Size: 11_357,
		SHA256: "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4",
	})
}

// countAstrLinkGuardDownload sends the HEAD the Hub counts as one download.
// It is best effort; the installation is already ready and never depends on it.
func (registry *Registry) countAstrLinkGuardDownload(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, astrLinkGuardCountTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, registry.probe.endpoint(
		astrLinkGuardRepoID, "resolve", astrLinkGuardCountRevision, "config.json",
	), nil)
	if err != nil {
		return
	}
	if response, err := registry.httpClient.Do(request); err == nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func defaultAstrLinkGuardLabelMapping() map[string]*contract.CanonicalKind {
	values := make(map[string]contract.CanonicalKind, len(astrLinkGuardKinds))
	for _, kind := range astrLinkGuardKinds {
		values[string(kind)] = kind
	}
	return resolvedLabelMapping(values)
}

// validateAstrLinkGuardConfig rejects releases whose labels or decoding
// contracts this build cannot run, so they are never offered as updates.
func validateAstrLinkGuardConfig(document []byte) error {
	config, adapter, err := parsePrivacyModelConfig(document)
	if err != nil || adapter != contract.PrivacyModelAdapterHFToken ||
		prohibitedModelConfig(config) ||
		!hasTokenClassificationArchitecture(config.Architectures) ||
		!usesTokenTypeIDs(config) ||
		len(config.ID2Label) != 1+2*len(astrLinkGuardKinds) ||
		config.ID2Label["0"] != "O" {
		return ErrUnsupportedModel
	}
	for index, kind := range astrLinkGuardKinds {
		if config.ID2Label[strconv.Itoa(1+index*2)] != "B-"+string(kind) ||
			config.ID2Label[strconv.Itoa(2+index*2)] != "I-"+string(kind) {
			return ErrUnsupportedModel
		}
	}
	var contracts map[string]json.RawMessage
	if json.Unmarshal(document, &contracts) != nil ||
		!knownGuardContract(contracts[astrLinkGuardDecoderContractField], astrLinkGuardDecoderContract) ||
		!knownGuardContract(contracts[astrLinkGuardSpanContractField], astrLinkGuardSpanContract) {
		return ErrUnsupportedModel
	}
	return nil
}

func knownGuardContract(raw json.RawMessage, known string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == known
}
