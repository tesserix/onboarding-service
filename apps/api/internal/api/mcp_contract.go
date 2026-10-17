package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"io"
	"strings"
)

// WriteMCPContract compiles the full tool catalog from the running server's typed schemas.
func WriteMCPContract(ctx context.Context, writer io.Writer) error {
	tools, err := mcpContract(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(tools)
}

func mcpContract(ctx context.Context) (*mcp.ListToolsResult, error) {
	server := (Deps{}).mcpServer(authz.Principal{ProductKey: "helivanta"}, auth.Claims{MCPRead: true, MCPCreate: true, MCPAdopt: true})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "onboarding-contract", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cs.Close() }()
	return cs.ListTools(ctx, nil)
}

// WriteMCPRegistry compiles Registry resources and schema fingerprints from the tool implementation.
func WriteMCPRegistry(ctx context.Context, writer io.Writer) error {
	catalog, err := mcpContract(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(catalog.Tools)
	if err != nil {
		return err
	}
	endpoint := "http://onboarding-api.onboarding.svc.cluster.local:8080/mcp"
	names := []string{}
	for _, tool := range catalog.Tools {
		names = append(names, tool.Name)
	}
	meta := func(name string) map[string]any {
		return map[string]any{"name": name, "namespace": "devai", "visibility": "internal", "labels": map[string]string{"devai.io/source": "onboarding-service", "mcp.tesserix.app/tenant": "onboarding", "mcp.tesserix.app/class": "platform", "mcp.devai.io/server": "operator-onboarding-mcp"}}
	}
	objects := []map[string]any{{"apiVersion": "registry.agentic.dev/v1alpha1", "kind": "MCPServer", "metadata": meta("operator-onboarding-mcp"), "spec": map[string]any{"name": "operator-onboarding-mcp", "displayName": "Operator Onboarding MCP", "description": "Authenticated operational hospital identity onboarding. Read and create roles are separate; adoption requires explicit additional authority. No clinical data or deletion tools.", "endpoint": endpoint, "remotes": []map[string]string{{"type": "streamableHttp", "url": endpoint}}, "protocolVersion": "2026-07-28", "authMode": "bearer", "tools": names, "toolSelector": map[string]any{"matchLabels": map[string]string{"mcp.devai.io/server": "operator-onboarding-mcp"}}, "x-tesserix": map[string]any{"source": "https://github.com/tesserix/onboarding-service", "transport": "stateless", "contractDigest": fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), "resourceAudience": "395476377970673709", "gatewayProject": "387190457387450503", "directAccess": false, "consumerProfile": "privileged-operator"}}}}
	for _, tool := range catalog.Tools {
		scope := "onboarding.helivanta.read"
		idempotency := "not_applicable"
		effects := []string{}
		if tool.Name == "request_hospital_onboarding" {
			idempotency = "caller_key"
			scope = "onboarding.helivanta.create"
			effects = []string{"identity.create"}
		}
		if tool.Name == "adopt_hospital_onboarding" {
			idempotency = "caller_key"
			scope = "onboarding.helivanta.adopt"
			effects = []string{"identity.adopt"}
		}
		metadata := meta("onboarding-" + strings.ReplaceAll(tool.Name, "_", "-"))
		metadata["annotations"] = map[string]string{"mcp.devai.io/wire-name": tool.Name}
		scopes := []string{"onboarding.helivanta.read"}
		if scope != scopes[0] {
			scopes = append(scopes, scope)
		}
		objects = append(objects, map[string]any{"apiVersion": "registry.agentic.dev/v1alpha1", "kind": "Tool", "metadata": metadata, "spec": map[string]any{"displayName": tool.Name, "description": tool.Description, "version": "1.0.0", "server": "operator-onboarding-mcp", "inputSchema": tool.InputSchema, "outputSchema": tool.OutputSchema, "annotations": tool.Annotations, "requiredScopes": scopes, "effects": effects, "idempotency": idempotency, "tags": []string{"onboarding", "operator", "mcp-tool"}}})
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	for i, obj := range objects {
		if i > 0 {
			if _, err = io.WriteString(writer, "---\n"); err != nil {
				return err
			}
		}
		if err = encoder.Encode(obj); err != nil {
			return err
		}
	}
	return nil
}
