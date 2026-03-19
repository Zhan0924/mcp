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

// Prompt 定义 MCP Prompt 接口
type Prompt interface {
	Definition() mcp.Prompt
	Handler() server.PromptHandlerFunc
}

// PromptProvider 提示词提供者接口
type PromptProvider interface {
	GetPrompts() []Prompt
}

// BasePrompt 基础 Prompt 实现
type BasePrompt struct {
	definition mcp.Prompt
	handler    server.PromptHandlerFunc
}

func NewBasePrompt(definition mcp.Prompt, handler server.PromptHandlerFunc) *BasePrompt {
	return &BasePrompt{
		definition: definition,
		handler:    handler,
	}
}

func (p *BasePrompt) Definition() mcp.Prompt {
	return p.definition
}

func (p *BasePrompt) Handler() server.PromptHandlerFunc {
	return p.handler
}

// Resource 定义 MCP Resource 接口
type Resource interface {
	Definition() mcp.Resource
	Handler() server.ResourceHandlerFunc
}

// ResourceProvider 资源提供者接口
type ResourceProvider interface {
	GetResources() []Resource
}

// BaseResource 基础 Resource 实现
type BaseResource struct {
	definition mcp.Resource
	handler    server.ResourceHandlerFunc
}

func NewBaseResource(definition mcp.Resource, handler server.ResourceHandlerFunc) *BaseResource {
	return &BaseResource{
		definition: definition,
		handler:    handler,
	}
}

func (r *BaseResource) Definition() mcp.Resource {
	return r.definition
}

func (r *BaseResource) Handler() server.ResourceHandlerFunc {
	return r.handler
}

// ResourceTemplate 定义 MCP ResourceTemplate 接口
type ResourceTemplate interface {
	Definition() mcp.ResourceTemplate
	Handler() server.ResourceTemplateHandlerFunc
}

// ResourceTemplateProvider 资源模板提供者接口
type ResourceTemplateProvider interface {
	GetResourceTemplates() []ResourceTemplate
}

// BaseResourceTemplate 基础 ResourceTemplate 实现
type BaseResourceTemplate struct {
	definition mcp.ResourceTemplate
	handler    server.ResourceTemplateHandlerFunc
}

func NewBaseResourceTemplate(definition mcp.ResourceTemplate, handler server.ResourceTemplateHandlerFunc) *BaseResourceTemplate {
	return &BaseResourceTemplate{
		definition: definition,
		handler:    handler,
	}
}

func (r *BaseResourceTemplate) Definition() mcp.ResourceTemplate {
	return r.definition
}

func (r *BaseResourceTemplate) Handler() server.ResourceTemplateHandlerFunc {
	return r.handler
}

// Registry 工具注册中心
type Registry struct {
	toolProviders             []ToolProvider
	promptProviders           []PromptProvider
	resourceProviders         []ResourceProvider
	resourceTemplateProviders []ResourceTemplateProvider
}

func NewRegistry() *Registry {
	return &Registry{
		toolProviders:             make([]ToolProvider, 0),
		promptProviders:           make([]PromptProvider, 0),
		resourceProviders:         make([]ResourceProvider, 0),
		resourceTemplateProviders: make([]ResourceTemplateProvider, 0),
	}
}

// RegisterProvider 注册提供者 (支持 ToolProvider, PromptProvider, ResourceProvider, ResourceTemplateProvider)
func (r *Registry) RegisterProvider(provider any) {
	if p, ok := provider.(ToolProvider); ok {
		r.toolProviders = append(r.toolProviders, p)
	}
	if p, ok := provider.(PromptProvider); ok {
		r.promptProviders = append(r.promptProviders, p)
	}
	if p, ok := provider.(ResourceProvider); ok {
		r.resourceProviders = append(r.resourceProviders, p)
	}
	if p, ok := provider.(ResourceTemplateProvider); ok {
		r.resourceTemplateProviders = append(r.resourceTemplateProviders, p)
	}
}

// ApplyToServer 将所有注册的工具、资源和提示词应用到 MCP 服务器
func (r *Registry) ApplyToServer(mcpServer *server.MCPServer) {
	for _, provider := range r.toolProviders {
		for _, tool := range provider.GetTools() {
			mcpServer.AddTool(tool.Definition(), tool.Handler())
		}
	}
	for _, provider := range r.promptProviders {
		for _, prompt := range provider.GetPrompts() {
			mcpServer.AddPrompt(prompt.Definition(), prompt.Handler())
		}
	}
	for _, provider := range r.resourceProviders {
		for _, resource := range provider.GetResources() {
			mcpServer.AddResource(resource.Definition(), resource.Handler())
		}
	}
	for _, provider := range r.resourceTemplateProviders {
		for _, template := range provider.GetResourceTemplates() {
			mcpServer.AddResourceTemplate(template.Definition(), template.Handler())
		}
	}
}
