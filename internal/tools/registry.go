package tools

import "github.com/meetflowstate/flowstate-telemetry/internal/config"

// ToolStatus represents the current state of a tool's telemetry configuration.
type ToolStatus struct {
	Detected   bool
	Configured bool
	Prompts    bool
	Details    string
}

// VerifyResult holds the outcome of a verify check against a tool.
type VerifyResult struct {
	OK      bool
	Message string
	Warning string
}

// Tool is the interface that each AI coding tool must implement
// for detection, installation, removal, and verification of
// telemetry configuration.
type Tool interface {
	Name() string
	Detect() bool
	Install(cfg config.Config) error
	Remove() error
	Status() ToolStatus
	Verify(cfg config.Config) VerifyResult
}

var registry []Tool

// Register adds a tool to the global registry.
func Register(t Tool) {
	registry = append(registry, t)
}

// All returns every registered tool.
func All() []Tool {
	return registry
}

// ByName looks up a tool by its canonical name.
func ByName(name string) (Tool, bool) {
	for _, t := range registry {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

func init() {
	Register(&ClaudeCode{})
	Register(&CopilotChat{})
	Register(&GeminiCLI{})
	Register(&CodexCLI{})
	Register(&QwenCode{})
	Register(&CursorTool{})
	Register(&WindsurfTool{})
	Register(&AiderTool{})
	Register(&OpenCodeTool{})
}
