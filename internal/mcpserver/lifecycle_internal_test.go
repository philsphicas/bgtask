package mcpserver

import (
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/philsphicas/bgtask/internal/taskservice"
)

func TestBatchResponse_RendersWholeCallError(t *testing.T) {
	result, out, err := batchResponse("all", nil, taskservice.Internal("stop", "", "", errors.New("store unavailable")))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || out.Error == nil {
		t.Fatalf("result = %+v, output = %+v; want an error result", result, out)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "internal:") {
		t.Fatalf("error text = %#v, want the top-level error", result.Content)
	}
}
