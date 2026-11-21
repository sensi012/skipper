package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMCPServerCreation(t *testing.T) {
	srv := NewMCPServer()
	if srv == nil {
		t.Fatalf("expected non-nil MCP server")
	}
}

func TestMCPHandleDoctor(t *testing.T) {
	req := mcp.CallToolRequest{}
	res, err := handleDoctor(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from handleDoctor: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected content in doctor tool result")
	}
}

func TestMCPHandleDiagnose(t *testing.T) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "skipper_diagnose",
			Arguments: map[string]interface{}{
				"workload": "cart-service",
				"logs":     "Out of memory: Killed process 42 exit code 137",
			},
		},
	}
	res, err := handleDiagnose(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from handleDiagnose: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("expected diagnostic findings")
	}
}
