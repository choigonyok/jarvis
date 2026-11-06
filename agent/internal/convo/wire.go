package convo

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/assets"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/checklist"
	"github.com/choigonyok/jarvis/agent/internal/module/goal"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/memory"
	"github.com/choigonyok/jarvis/agent/internal/module/messages"
	mindmod "github.com/choigonyok/jarvis/agent/internal/module/mind"
	photolib "github.com/choigonyok/jarvis/agent/internal/module/photos"
	"github.com/choigonyok/jarvis/agent/internal/module/place"
	"github.com/choigonyok/jarvis/agent/internal/module/remind"
	"github.com/choigonyok/jarvis/agent/internal/module/show"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
	"github.com/choigonyok/jarvis/agent/internal/module/web"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/tool"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

// wired is the operator's conversation as cmd/agent puts it up: the modules
// (for the gate's cards), the two boards and the module servers with the tools
// they offer, the jobs (for what passes without a card) and long-term memory
// (for what is recalled in front of the message).
type wired struct {
	modules *module.Registry
	boards  map[string]*tool.Board
	servers map[string][]*mcp.Tool
	jobs    *jobs.Module
	memory  *memory.Module
}

// wire mirrors cmd/agent/main.go for the operator, in its order - the rows of
// a board are said in the order modules add them. Keep the two in step: a
// module the agent puts up and this does not is one the tests do not see
// (TestEveryModuleIsWired).
func wire(ctx context.Context, cfg *config.Config, store *thread.Store, events *bus.Bus, log *slog.Logger) (*wired, error) {
	w := &wired{modules: module.NewRegistry(), boards: map[string]*tool.Board{}, servers: map[string][]*mcp.Tool{}}
	handlers := map[string]http.Handler{}
	look := tool.New(tool.Look, tool.LookAbout)
	change := tool.New(tool.Change, tool.ChangeAbout)
	mount := func(name string, h http.Handler) {
		handlers["/mcp/"+name] = h
		cfg.ModuleMCPURLs[name] = cfg.PublicMCPURL + "/" + name
	}

	calendarModule := calendar.New(calendar.NewStore(cfg.CalendarURL, cfg.APIToken, events))
	w.modules.Add(calendarModule)
	calendarModule.Look(look)
	mount(calendar.ServerName, calendarModule.Handler())

	photos, err := uploads.New(cfg.UploadsDir)
	if err != nil {
		return nil, err
	}
	assetsModule := assets.New(assets.NewStore(cfg.AssetsURL, cfg.APIToken))
	w.modules.Add(assetsModule)
	spendingModule := spending.New(spending.NewStore(cfg.MindURL, cfg.APIToken, nil))
	w.modules.Add(spendingModule)
	assetsModule.Look(look)
	spendingModule.Look(look)
	vaultModule := vault.New(vault.NewClient(cfg.VaultURL, cfg.APIToken), log)
	w.modules.Add(vaultModule)
	mount(vault.ServerName, vaultModule.Handler())
	vaultModule.Look(look)

	w.jobs = jobs.New(jobs.NewStore(cfg.MindURL+jobs.Prefix, cfg.APIToken), photos, proposal.NewStore(events), store, log)
	w.modules.Add(w.jobs)
	w.jobs.SetVault(vaultModule)
	w.jobs.Look(look)
	mount(jobs.ServerName, w.jobs.Handler())

	w.memory = memory.New(memory.NewStore(cfg.MemoryURL, cfg.APIToken, "owner"))
	w.modules.Add(w.memory)
	w.memory.Look(look)

	webModule := web.New(web.NewClient(cfg.SearchURL, cfg.SearchToken))
	w.modules.Add(webModule)
	mount(web.ServerName, webModule.Handler())
	cfg.Allow(web.Tools...)

	showModule := show.New(photos, store, webModule.Seen, log)
	w.modules.Add(showModule)
	mount(show.ServerName, showModule.Handler())
	cfg.Allow(show.Tools...)

	library := photolib.New(cfg.PhotosURL, cfg.MindURL, cfg.APIToken, photos, log)
	w.modules.Add(library)
	library.Look(look)

	placeModule := place.New(cfg.ContextURL, cfg.APIToken)
	placeModule.UseNaver(webModule.Client())
	w.modules.Add(placeModule)
	placeModule.Look(look)

	mindModule := mindmod.New(mindmod.Client{URL: cfg.MindURL, Token: cfg.APIToken})
	w.modules.Add(mindModule)
	mindModule.Look(look)
	mindModule.LookRules(look)
	mindModule.Change(change)
	messagesModule := messages.New(cfg.MindURL, cfg.APIToken)
	w.modules.Add(messagesModule)
	messagesModule.Look(look)

	remindModule := remind.New(cfg.MindURL, cfg.APIToken, nil)
	remindModule.UseMap(placeModule)
	w.modules.Add(remindModule)
	remindModule.Look(look)
	remindModule.Change(change)
	checklistModule := checklist.New(cfg.MindURL, cfg.APIToken, nil, store, events, log)
	w.modules.Add(checklistModule)
	checklistModule.Look(look)
	checklistModule.Change(change)
	goalModule := goal.New(cfg.MindURL, cfg.APIToken, nil)
	w.modules.Add(goalModule)
	goalModule.Look(look)
	goalModule.Change(change)

	for name, board := range map[string]*tool.Board{tool.Look: look, tool.Change: change} {
		w.boards["/mcp/"+name] = board
		cfg.ModuleMCPURLs[name] = cfg.PublicMCPURL + "/" + name
		cfg.Allow(tool.Qualified(name))
	}
	cfg.Allow("Skill")

	for path, h := range handlers {
		tools, err := toolsOf(ctx, h)
		if err != nil {
			return nil, fmt.Errorf("%s 의 도구를 읽지 못했습니다: %w", path, err)
		}
		w.servers[path] = tools
	}
	return w, nil
}
