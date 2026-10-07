package config

import (
	"strings"
	"testing"
)

// The guest agent's limits are what keep the operator's money and messages out
// of its reach, so they are pinned here rather than left to the compose file.
func TestGuestRole(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_ROLE", "guest")
	t.Setenv("JARVIS_EXTRA_MCP", "mcp-browser=http://browser:8931/mcp")
	t.Setenv("JARVIS_MCP_TOKEN", "tok")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Guest || c.Thread != "guest" {
		t.Fatalf("guest=%v thread=%q", c.Guest, c.Thread)
	}
	if c.BuiltinTools == nil || strings.Contains(*c.BuiltinTools, "Bash") || strings.Contains(*c.BuiltinTools, "Read") {
		t.Fatalf("내장 도구가 제한되지 않았습니다: %v", c.BuiltinTools)
	}
	if len(c.ExtraMCPURLs) != 0 || c.Enrich {
		t.Fatalf("브라우저나 백그라운드 조회가 켜져 있습니다: %v %v", c.ExtraMCPURLs, c.Enrich)
	}
	for _, tool := range c.AllowedTools {
		if strings.Contains(tool, "assets") || strings.Contains(tool, "spending") || strings.Contains(tool, "browser") {
			t.Fatalf("손님에게 허용된 도구: %s", tool)
		}
	}
	if strings.Contains(c.SystemPrompt, "get_portfolio") || strings.Contains(c.SystemPrompt, "get_spending") {
		t.Fatal("손님 지시문에 자산·가계부 도구가 있습니다")
	}
}

func TestOwnerUnchanged(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_ROLE", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Guest || c.Thread != "" || c.BuiltinTools != nil {
		t.Fatalf("운영자 설정이 바뀌었습니다: guest=%v thread=%q tools=%v", c.Guest, c.Thread, c.BuiltinTools)
	}
}
