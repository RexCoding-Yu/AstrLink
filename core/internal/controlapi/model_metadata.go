package controlapi

import (
	"context"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/modelmeta"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const ModelMetadataPath = "/control/v1/model-metadata"

// getModelMetadata answers what LLM Metadata says about the official model
// a gateway model reaches.
func (h *Handler) getModelMetadata(w http.ResponseWriter, r *http.Request) {
	if h.pricingStore == nil {
		writeError(w, 503, "model_metadata_unavailable", "model metadata is unavailable")
		return
	}
	q := r.URL.Query()
	model := q.Get("model")
	if len(q) != 1 || len(q["model"]) != 1 || model == "" || len(model) > 256 {
		writeError(w, 400, "invalid_query", "model is required")
		return
	}
	value, ok, err := h.officialModel(r.Context(), model)
	if err != nil {
		h.writeStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, 404, "model_metadata_not_found", "LLM Metadata lists no official model for this model")
		return
	}
	writeJSON(w, 200, value)
}

// officialModel finds the official model a gateway model reaches: after the
// routing redirect, through the price binding of each enabled service that
// serves it, as billing values its calls, and last by the model's own name.
func (h *Handler) officialModel(ctx context.Context, model string) (modelmeta.Model, bool, error) {
	catalog, err := h.pricingStore.PricingCatalog(ctx)
	if err != nil {
		return modelmeta.Model{}, false, err
	}
	if h.routingSettings != nil {
		settings, err := h.routingSettings.GetRoutingSettings(ctx)
		if err != nil {
			return modelmeta.Model{}, false, err
		}
		if redirect, ok := contract.ResolveModelRedirect(settings.ModelRedirects, model); ok && redirect.To != "" {
			model = redirect.To
		}
	}
	options := storage.ServiceListOptions{Limit: 200}
	for {
		page, err := h.serviceStore.ListServices(ctx, options)
		if err != nil {
			return modelmeta.Model{}, false, err
		}
		for _, record := range page.Items {
			upstream, served := record.Service.UpstreamModelFor(model)
			if !record.Service.Enabled || !served {
				continue
			}
			config, err := h.pricingStore.PricingConfig(ctx, record.Service.ID)
			if err != nil {
				return modelmeta.Model{}, false, err
			}
			if found, ok := config.ResolveModel(upstream, catalog.Models); ok {
				return found, true, nil
			}
		}
		if page.NextCursor == "" || page.NextCursor == options.Cursor {
			break
		}
		options.Cursor = page.NextCursor
	}
	found, ok := pricing.Config{}.ResolveModel(model, catalog.Models)
	return found, ok, nil
}
