package adapter

import "testing"

func TestEnumValid(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"sdk kind", SDKKindOpenAI.Valid()},
		{"protocol", ProtocolAnthropic.Valid()},
		{"retry action", RetryNextProvider.Valid()},
		{"auth kind", AuthBearerHeader.Valid()},
		{"capability", CapabilityThinking.Valid()},
		{"thinking effort", ThinkingXHigh.Valid()},
		{"request action", RequestClampNumber.Valid()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.valid {
				t.Error("Valid() = false, want true")
			}
		})
	}
}

func TestUnknownEnumsAreInvalid(t *testing.T) {
	if SDKKind("unknown").Valid() {
		t.Error("unknown SDKKind is valid")
	}
	if Protocol("unknown").Valid() {
		t.Error("unknown Protocol is valid")
	}
	if RetryAction("unknown").Valid() {
		t.Error("unknown RetryAction is valid")
	}
	if AuthKind("unknown").Valid() {
		t.Error("unknown AuthKind is valid")
	}
	if Capability("unknown").Valid() {
		t.Error("unknown Capability is valid")
	}
	if ThinkingEffort("unknown").Valid() {
		t.Error("unknown ThinkingEffort is valid")
	}
	if RequestAction("unknown").Valid() {
		t.Error("unknown RequestAction is valid")
	}
}
