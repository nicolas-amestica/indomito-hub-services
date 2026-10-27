package domain

import "testing"

func TestDefaultAgentManagedConfigPreservesCurrentCapabilities(t *testing.T) {
	config := DefaultAgentManagedConfig()
	if err := validateManagedConfig(config); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
	if !config.Policies.OfferMeetings || !config.Policies.HumanHandoffEnabled || !config.Policies.HandoffWhatsAppEnabled || !config.Policies.HandoffEmailEnabled {
		t.Fatal("current meeting and handoff capabilities must remain enabled")
	}
	if config.Policies.HandoffPhone != "56963552893" || config.Policies.HandoffEmail != "info@girasindomito.cl" {
		t.Fatal("current recipients must be preserved as initial managed values")
	}
	if len(config.Knowledge) < 4 {
		t.Fatal("current commercial knowledge must be seeded")
	}
}

func TestManagedConfigRequiresAnEnabledHandoffChannel(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.Policies.HandoffWhatsAppEnabled = false
	config.Policies.HandoffEmailEnabled = false
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected handoff channel validation")
	}
}

func TestManagedConfigValidatesHandoffDestinations(t *testing.T) {
	config := DefaultAgentManagedConfig()
	config.Policies.HandoffPhone = "invalid"
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected invalid phone")
	}
	config = DefaultAgentManagedConfig()
	config.Policies.HandoffEmail = "invalid"
	if err := validateManagedConfig(config); err == nil {
		t.Fatal("expected invalid email")
	}
}
