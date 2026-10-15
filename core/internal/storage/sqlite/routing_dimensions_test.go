package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func dimensionRoutingSettings() contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{{
		ID: "superclaude", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fast", ServiceID: "service_a", Model: "model_a"},
			{ID: "strong", ServiceID: "service_a", Model: "model_b"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "billing", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "topic", In: []string{"billing"}},
			TargetMemberID: "strong",
		}},
	}}
	return settings
}

// A rule naming a custom dimension must survive the store's own write and
// load validation, not only the control API's precheck. The store used to
// validate with no dimensions, so the API accepted the save and the store
// rejected it with an opaque invalid-argument error.
func TestRoutingSettingsWithCustomDimensionRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "dimensions.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}

	// Without the dimension declared the rule is still refused: the store
	// reads the strict, built-in-only field set.
	if err := store.UpdateRoutingSettings(ctx, dimensionRoutingSettings()); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("undeclared dimension save err = %v, want ErrInvalidArgument", err)
	}

	classifier := contract.ClassifierSettings{
		ClassifierSource: contract.ClassifierSourceInline,
		Dimensions: []contract.ClassifierDimension{{
			Name:    "topic",
			Options: []contract.ClassifierDimensionOption{{Name: "billing"}, {Name: "support"}},
		}},
	}
	if err := store.UpdateClassifierSettings(ctx, classifier); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRoutingSettings(ctx, dimensionRoutingSettings()); err != nil {
		t.Fatalf("declared dimension save: %v", err)
	}
	loaded, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.ModelRoutingGroups) != 1 || loaded.ModelRoutingGroups[0].Rules[0].When.Field != "topic" {
		t.Fatalf("loaded groups = %+v", loaded.ModelRoutingGroups)
	}
	// A service write re-validates the stored routing document inside its
	// transaction; that path must also see the dimension.
	if _, err := store.CreateService(ctx, pathTestService("service_b"), storage.CredentialMutation{}); err != nil {
		t.Fatalf("service write after dimension rule: %v", err)
	}
}
