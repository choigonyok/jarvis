package claudecode

import (
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/config"
)

// The agent's own MCP servers refuse callers without the internal token, so
// every entry pointing at them - chat or background - must carry it, and the
// outside servers must not.
func TestInternalMCPToken(t *testing.T) {
	r := &Runner{cfg: config.Config{
		PublicMCPURL:     "http://127.0.0.1:8080/mcp",
		ModuleMCPURLs:    map[string]string{"calendar": "http://127.0.0.1:8080/mcp/calendar"},
		ExtraMCPURLs:     map[string]string{"mcp-browser": "http://b"},
		ExtraMCPToken:    "outer",
		InternalMCPToken: "inner",
	}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	var chat struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(r.mcpConfig()), &chat); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"jarvis", "calendar"} {
		if got := chat.MCPServers[name].Headers["Authorization"]; got != "Bearer inner" {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := chat.MCPServers["mcp-browser"].Headers["Authorization"]; got != "Bearer outer" {
		t.Errorf("browser: %q", got)
	}

	args, err := r.backgroundArgs(Task{Name: "t", Servers: map[string]string{"suggest": "http://127.0.0.1:8080/mcp/suggest"}})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--mcp-config")
	var bg struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if i < 0 || json.Unmarshal([]byte(args[i+1]), &bg) != nil || bg.MCPServers["suggest"].Headers["Authorization"] != "Bearer inner" {
		t.Fatalf("background MCP config: %v", args)
	}
}
