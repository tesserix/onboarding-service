package api

import (
	"bytes"
	"os"
	"testing"
)

func TestMCPCompiledContractsMatchThePublishedArtifacts(t *testing.T) {
	for _, name := range []string{"operator-onboarding-mcp.json", "operator-onboarding-registry.yaml"} {
		t.Run(name, func(t *testing.T) {
			var actual bytes.Buffer
			write := WriteMCPContract
			if name == "operator-onboarding-registry.yaml" {
				write = WriteMCPRegistry
			}
			if err := write(t.Context(), &actual); err != nil {
				t.Fatal(err)
			}
			expected, err := os.ReadFile("../../../../contracts/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(expected, actual.Bytes()) {
				t.Fatal("compiled MCP contract drift; regenerate with cmd/mcp-contract")
			}
		})
	}
}
