package stream

import (
	"errors"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/nonstream"
)

func TestSafeSentinelsAreCanonicalNonStreamAliases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ got, want error }{
		{ErrModelNotFound, nonstream.ErrModelNotFound},
		{ErrInvalidRequest, nonstream.ErrInvalidRequest},
		{ErrUnauthorized, nonstream.ErrUnauthorized},
		{ErrMisconfigured, nonstream.ErrMisconfigured},
	} {
		if !errors.Is(tc.got, tc.want) {
			t.Fatalf("stream sentinel %v is not canonical %v", tc.got, tc.want)
		}
	}
}
