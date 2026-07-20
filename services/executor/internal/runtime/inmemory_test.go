package runtime

import "testing"

func TestInMemoryContract(t *testing.T) {
	ContractTests(t, func(version string) Port { return NewInMemory(version) })
}

func TestInMemoryContractRealtime(t *testing.T) {
	ContractTestsRealtime(t, func(version string) Port { return NewInMemory(version) })
}

func TestInMemoryContractExtra(t *testing.T) {
	ContractTestsInMemory(t)
}
