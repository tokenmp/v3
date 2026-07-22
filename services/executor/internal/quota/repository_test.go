package quota

import (
	"context"
	"errors"
	"testing"
)

func testReserve(id ReservationID) ReserveRequest {
	return ReserveRequest{ID: id, Metadata: Metadata{RequestID: "req_123", Subject: "subject-1", KeyID: "key-1", Protocol: "openai", Model: "model-1", ProviderID: "provider-1", RouteID: "route-1", CredentialID: "credential-1", AdapterID: "adapter-1", Revision: "revision-1", Generation: 1}, Estimate: Estimate{Basis: BasisNone}}
}
func testID() ReservationID { return "res_1234567890123456" }
func TestRepositoryExactReplayAndTerminalConflict(t *testing.T) {
	for _, repo := range []Repository{NewDomainInMemory(), NewTypedMock()} {
		in := testReserve(testID())
		if _, err := repo.ReserveReservation(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ReserveReservation(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.FinalizeReservation(context.Background(), FinalizeRequest{ID: in.ID, Outcome: FinalizeOutcome{Disposition: AccountingUnpricedSuccess}}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ReleaseReservation(context.Background(), ReleaseRequest{ID: in.ID, Reason: ReleaseFailed}); !errors.Is(err, ErrConflict) {
			t.Fatalf("%T: %v", repo, err)
		}
	}
}
func TestRepositoryFinalizesTypedOutcome(t *testing.T) {
	repo := NewDomainInMemory()
	in := testReserve(testID())
	if _, err := repo.ReserveReservation(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FinalizeReservation(context.Background(), FinalizeRequest{ID: in.ID, Outcome: FinalizeOutcome{Disposition: AccountingConfirmedUsage, Outcome: OutcomeAfterCommitError, Usage: ConfirmedUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}})
	if err != nil || got.Settlement.Outcome == nil || got.Settlement.Outcome.Outcome != OutcomeAfterCommitError {
		t.Fatalf("finalize=%v err=%v", got, err)
	}
}
func TestRepositoryRejectsInvalidBeforeWrite(t *testing.T) {
	repo := NewDomainInMemory()
	in := testReserve("bad")
	if _, err := repo.ReserveReservation(context.Background(), in); !errors.Is(err, ErrInvalidReservation) {
		t.Fatal(err)
	}
	if repo.Count() != 0 {
		t.Fatal("write")
	}
}

func TestMetadataValidationAndConfirmedUsageTotals(t *testing.T) {
	valid := testReserve(testID()).Metadata
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid metadata: %v", err)
	}
	for _, mutate := range []func(*Metadata){
		func(m *Metadata) { m.RequestID = "contains space" },
		func(m *Metadata) { m.Subject = "" },
		func(m *Metadata) { m.ProviderID = ""; m.InitialCandidate = "" },
	} {
		m := valid
		mutate(&m)
		if err := m.Validate(); !errors.Is(err, ErrInvalidMetadata) {
			t.Fatalf("metadata validation=%v, want invalid metadata", err)
		}
	}
	if (ConfirmedUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 4}).Valid() {
		t.Fatal("inconsistent total accepted")
	}
	if !(ConfirmedUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}).Valid() {
		t.Fatal("exact total rejected")
	}
}
