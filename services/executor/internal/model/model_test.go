package model

import "testing"

func TestReservationStatusIsTerminal(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		status ReservationStatus
		want   bool
	}{
		"reserved is not terminal": {status: StatusReserved, want: false},
		"finalized is terminal":    {status: StatusFinalized, want: true},
		"released is terminal":     {status: StatusReleased, want: true},
		"empty is not terminal":    {status: "", want: false},
		"unknown is not terminal":  {status: "unknown", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := test.status.IsTerminal(); got != test.want {
				t.Errorf("IsTerminal(%q) = %v, want %v", test.status, got, test.want)
			}
		})
	}
}
