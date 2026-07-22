package quota

import (
	"context"
	"testing"
)

func FuzzDomainInMemoryRejectsMalformedReservationWithoutWrite(f *testing.F) {
	f.Add("res_aaaaaaaaaaaaaaaa", "subject-1", "none")
	f.Add("bad", " space", "future")
	f.Fuzz(func(t *testing.T, rawID, subject, basis string) {
		repo := NewDomainInMemory()
		in := typedReservation(ReservationID(rawID))
		in.Metadata.Subject = subject
		in.Estimate.Basis = EstimateBasis(basis)
		got, err := repo.ReserveReservation(context.Background(), in)
		if err != nil {
			if count := repo.Count(); count != 0 {
				t.Fatalf("failed reserve wrote %d record(s): %v", count, err)
			}
			return
		}
		if got.ID != in.ID || got.Metadata.Subject != subject || got.Estimate.Basis != EstimateBasis(basis) {
			t.Fatalf("stored reservation differs from accepted input (state=%q)", got.State)
		}
		if _, err := repo.Lookup(context.Background(), in.ID); err != nil {
			t.Fatalf("accepted reservation lookup: %v", err)
		}
	})
}
