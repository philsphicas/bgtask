package mcpserver

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func errorResult(err *ToolError) *mcp.CallToolResult {
	text := "bgtask operation failed"
	if err != nil {
		text = fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	result := textResult(text)
	result.IsError = true
	return result
}
