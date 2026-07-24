package settings

import "testing"

func TestGetReturnsDefaults(t *testing.T) {
	s := NewStore()
	got := s.Get("nobody")
	if got.PreferredBilling != DefaultPreferredBilling || got.FallbackEnabled != DefaultFallbackEnabled {
		t.Errorf("defaults = %+v", got)
	}
}

func TestSnapshotUpdatesOnlyProvidedFields(t *testing.T) {
	s := NewStore()
	// 只更新 preferredBilling。
	pb := "token"
	got := s.Snapshot("u1", &pb, nil)
	if got.PreferredBilling != "token" || got.FallbackEnabled != DefaultFallbackEnabled {
		t.Errorf("snapshot1 = %+v", got)
	}
	// 只把 fallbackEnabled 显式设为 false。
	fe := false
	got = s.Snapshot("u1", nil, &fe)
	if got.PreferredBilling != "token" || got.FallbackEnabled != false {
		t.Errorf("snapshot2 = %+v", got)
	}
	// 持久化生效。
	if s.Get("u1").FallbackEnabled != false {
		t.Errorf("expected false persisted")
	}
}

func TestSnapshotIgnoresEmptyPreferredBilling(t *testing.T) {
	s := NewStore()
	pb := "token"
	s.Snapshot("u1", &pb, nil)
	empty := ""
	got := s.Snapshot("u1", &empty, nil)
	if got.PreferredBilling != "token" {
		t.Errorf("empty preferredBilling should not clear existing, got %q", got.PreferredBilling)
	}
}
