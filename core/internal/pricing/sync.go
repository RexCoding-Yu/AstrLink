package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/modelmeta"
)

type CatalogStore interface {
	PricingCatalog(context.Context) (Catalog, error)
	SavePricingCatalog(context.Context, Catalog) error
}
type Manager struct {
	store     CatalogStore
	source    *modelmeta.Source
	mu        sync.Mutex
	syncMu    sync.Mutex
	checked   *time.Time
	lastError string
}

func NewManager(store CatalogStore, client *http.Client) *Manager {
	return &Manager{store: store, source: modelmeta.NewSource(client)}
}
func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status(ctx)
}
func (m *Manager) status(ctx context.Context) (Status, error) {
	c, err := m.store.PricingCatalog(ctx)
	if err != nil {
		return Status{}, err
	}
	s := Status{Source: modelmeta.SourceURL, Version: c.Version, CheckedAt: m.checked, Error: m.lastError, Models: len(c.Prices), Warnings: c.Warnings}
	if s.Warnings == nil {
		s.Warnings = []string{}
	}
	if !c.ActivatedAt.IsZero() {
		s.UpdatedAt = &c.ActivatedAt
	}
	return s, nil
}
func (m *Manager) Sync(ctx context.Context) (status Status, err error) {
	if !m.syncMu.TryLock() {
		return Status{}, fmt.Errorf("price sync already in progress")
	}
	defer m.syncMu.Unlock()
	m.mu.Lock()
	now := time.Now().UTC()
	m.checked = &now
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if err != nil {
			m.lastError = err.Error()
		} else {
			m.lastError = ""
		}
		m.mu.Unlock()
		status, _ = m.Status(ctx)
	}()
	old, err := m.store.PricingCatalog(ctx)
	if err != nil {
		return status, err
	}
	// A catalog saved before model metadata was kept is fetched again.
	etag := old.ETag
	if old.Models == nil {
		etag = ""
	}
	manifest, etag, unchanged, err := m.source.Get(ctx, "manifest.json", etag)
	if err != nil || unchanged {
		return status, err
	}
	var meta struct {
		Version     int       `json:"version"`
		GeneratedAt time.Time `json:"generatedAt"`
		Warnings    []string  `json:"warnings"`
	}
	if json.Unmarshal(manifest, &meta) != nil || meta.Version != 1 || meta.GeneratedAt.IsZero() {
		return status, fmt.Errorf("unsupported pricing manifest")
	}
	all, _, _, err := m.source.Get(ctx, "all.json", "")
	if err != nil {
		return status, err
	}
	providers, err := modelmeta.Parse(all)
	if err != nil {
		return status, err
	}
	prices := []Price{}
	models := []modelmeta.Model{}
	warnings := append([]string{}, meta.Warnings...)
	parts := []string{string(manifest), string(all)}
	ids := []string{}
	for id := range providers {
		if Providers[id] != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		body, _, _, e := m.source.Get(ctx, "newapi/providers/"+id+"/ratio_config-v1-base.json", "")
		if e != nil {
			return status, e
		}
		var rules struct {
			Success bool `json:"success"`
			Data    struct {
				Expressions map[string]string `json:"billing_expr"`
				Modes       map[string]string `json:"billing_mode"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &rules) != nil || !rules.Success {
			return status, fmt.Errorf("invalid provider prices: %s", id)
		}
		parts = append(parts, string(body))
		names := map[string]string{}
		for _, model := range providers[id] {
			names[model.Model] = model.Name
		}
		priced := []string{}
		for model := range rules.Data.Expressions {
			priced = append(priced, model)
		}
		sort.Strings(priced)
		for _, model := range priced {
			expr := rules.Data.Expressions[model]
			if len(model) > 256 || rules.Data.Modes[model] != "tiered_expr" {
				return status, fmt.Errorf("unsupported provider price: %s", id)
			}
			if e := ValidateExpression(expr); e != nil {
				warnings = append(warnings, id+"/"+model+": unsupported billing expression")
				continue
			}
			prices = append(prices, Price{Provider: id, Model: model, Name: names[model], Expression: expr})
		}
		models = append(models, providers[id]...)
	}
	if len(prices) == 0 {
		return status, fmt.Errorf("no official API prices found")
	}
	// Reject a deployment that changed during the multi-file download.
	after, _, _, err := m.source.Get(ctx, "manifest.json", "")
	if err != nil {
		return status, err
	}
	if string(after) != string(manifest) {
		return status, fmt.Errorf("price publication changed; retry sync")
	}
	c := Catalog{Version: Digest(parts...), GeneratedAt: meta.GeneratedAt, ActivatedAt: time.Now().UTC(), ETag: etag, Prices: prices, Models: models, Warnings: warnings}
	err = m.store.SavePricingCatalog(ctx, c)
	return status, err
}

// Run synchronizes in Core, independently of any frontend page lifecycle.
func (m *Manager) Run(ctx context.Context, logf func(string, ...any)) {
	for {
		c, err := m.store.PricingCatalog(ctx)
		m.mu.Lock()
		checked := c.ActivatedAt
		if m.checked != nil && m.checked.After(checked) {
			checked = *m.checked
		}
		due := err != nil || m.lastError != "" || checked.IsZero() || time.Since(checked) >= 24*time.Hour || c.Models == nil
		m.mu.Unlock()
		if due {
			syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_, err = m.Sync(syncCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				logf("price sync: %v", err)
			}
		}
		// Fill missing prices and value entries earlier releases left unpriced,
		// even when the catalog has not changed. Already valued entries stay fixed.
		if pending, ok := m.store.(interface {
			PriceUnpriced(context.Context) (int, error)
		}); ok && ctx.Err() == nil {
			if _, err := pending.PriceUnpriced(ctx); err != nil && ctx.Err() == nil {
				logf("pending price valuation: %v", err)
			}
		}
		timer := time.NewTimer(5 * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
