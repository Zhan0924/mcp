/*
┌──────────────────────────────────────────────────────────────────────────────┐
│ registry.go — MCP 工具注册表                                                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标:                                                                        │
│  - 将 ToolProvider 与 MCP Server 解耦                                       │
│  - 便于单元测试与扩展多个工具组                                              │
│                                                                              │
│ 结构:                                                                        │
│  - Tool / ToolProvider 接口定义                                              │
│  - Registry: 收集 Provider，并一次性注册到 MCP Server                       │
└──────────────────────────────────────────────────────────────────────────────┘
*/
package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Tool 定义 MCP 工具接口
type Tool interface {
	Definition() mcp.Tool
	Handler() func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// ToolProvider 工具提供者接口
type ToolProvider interface {
	GetTools() []Tool
}

// BaseTool 基础工具实现
type BaseTool struct {
	definition mcp.Tool
	handler    func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// NewBaseTool 创建基础工具
func NewBaseTool(
	definition mcp.Tool,
	handler func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error),
) *BaseTool {
	return &BaseTool{
		definition: definition,
		handler:    handler,
	}
}

func (t *BaseTool) Definition() mcp.Tool {
	return t.definition
}

func (t *BaseTool) Handler() func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return t.handler
}

// Registry 工具注册中心
type Registry struct {
	providers []ToolProvider
}

// NewRegistry 创建工具注册中心
func NewRegistry() *Registry {
	return &Registry{
		providers: make([]ToolProvider, 0),
	}
}

// RegisterProvider 注册工具提供者
func (r *Registry) RegisterProvider(provider ToolProvider) {
	r.providers = append(r.providers, provider)
}

// ApplyToServer 将所有注册的工具应用到 MCP 服务器
func (r *Registry) ApplyToServer(mcpServer *server.MCPServer) {
	// 统一注册入口，避免在 main/server 中散落 AddTool 调用
	for _, provider := range r.providers {
		for _, tool := range provider.GetTools() {
			mcpServer.AddTool(tool.Definition(), tool.Handler())
		}
	}
}
