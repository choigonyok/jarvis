package convo

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/choigonyok/jarvis/agent/internal/assemble"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
)

// wire puts the operator's agent together as cmd/agent does - with the one
// assembly (internal/assemble), the scenario's fakes handed in through its
// Deps - and serves on mux what the conversation is given, at the paths it
// is given them: the permission gate itself, and in place of each MCP server
// in the conversation's config a stand-in with the same tools and arguments,
// answered from the scenario (answer).
func wire(ctx context.Context, cfg *config.Config, d assemble.Deps, mux *http.ServeMux, answer func(key string) string) (*assemble.Assembly, error) {
	agent, err := assemble.Assemble(cfg, d)
	if err != nil {
		return nil, err
	}
	mux.Handle("/mcp", agent.Mounts["/mcp"])
	for _, name := range slices.Sorted(maps.Keys(cfg.ModuleMCPURLs)) {
		path := "/mcp/" + name
		if board, ok := agent.Boards[path]; ok {
			mux.Handle(path, board.Stand(func(_ context.Context, what string, _ json.RawMessage) (string, error) {
				return answer(name + ":" + what), nil
			}).Handler())
			continue
		}
		server, ok := agent.Mounts[path]
		if !ok {
			return nil, fmt.Errorf("%s 는 대화에 주어지는데 세워지지 않았습니다", path)
		}
		tools, err := toolsOf(ctx, server)
		if err != nil {
			return nil, fmt.Errorf("%s 의 도구를 읽지 못했습니다: %w", path, err)
		}
		mux.Handle(path, stand(name, tools, func(tool string, _ json.RawMessage) string { return answer(tool) }))
	}
	// The shared browser is a server of its own outside the agent (mcp-browser).
	mux.Handle("/browser", stand(jobs.BrowserServer, browserTools(), func(tool string, _ json.RawMessage) string { return answer(tool) }))
	return agent, nil
}
