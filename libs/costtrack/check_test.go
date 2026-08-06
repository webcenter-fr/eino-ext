package costtrack

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestTracker_Check_OK(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer func() { _ = bus.Close() }()

	holder := new(atomic.Pointer[modelsdev.Catalog])
	holder.Store(&modelsdev.Catalog{})

	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	results := tracker.Check(context.Background())
	if !results.OK() {
		t.Errorf("Check OK = false, want true; results: %s", results.JSON(""))
	}
}

func TestTracker_Check_NoCatalog(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer func() { _ = bus.Close() }()

	holder := new(atomic.Pointer[modelsdev.Catalog])

	tracker, err := NewTracker(context.Background(), &Config{
		Bus:             bus,
		PricingProvider: "anthropic",
		Resolve:         func(gw string) (string, string, bool) { return "", "", false },
		CatalogHolder:   holder,
	})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}

	results := tracker.Check(context.Background())
	if !results.OK() {
		t.Errorf("Check OK = false, want true (nil catalog => limited, not error); results: %s", results.JSON(""))
	}
	foundLimited := false
	for _, r := range results {
		if r.Instance == "catalog" && r.Status == checkup.StatusLimited {
			foundLimited = true
		}
	}
	if !foundLimited {
		t.Errorf("expected a limited catalog result when catalog is nil")
	}
}
