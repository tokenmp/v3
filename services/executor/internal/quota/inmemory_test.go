package quota

import (
	"testing"
)

func TestInMemoryContract(t *testing.T) {
	ContractTests(t, func() Port { return NewInMemory() })
}
