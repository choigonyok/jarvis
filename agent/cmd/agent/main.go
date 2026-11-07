package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// The IANA database, embedded. The runtime image has no tzdata, so
	// without this a TZ like Asia/Seoul would silently resolve to UTC - and
	// every "오늘" the agent computes would be a day off after midnight.
	_ "time/tzdata"

	"github.com/choigonyok/jarvis/agent/internal/adapter/permission"
	"github.com/choigonyok/jarvis/agent/internal/assemble"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/policy"
)

func main() {
	// The CLI runs this binary before every tool call: it asks the running
	// agent and exits, with no configuration of its own.
	if len(os.Args) > 1 && os.Args[1] == policy.HookCommand {
		os.Exit(policy.RunHook(os.Stdin, os.Stdout, os.Getenv("JARVIS_POLICY_URL"), os.Getenv("JARVIS_POLICY_TOKEN"), http.DefaultClient))
	}
	// The conversation's tests on the real model (scripts/convo).
	if len(os.Args) > 1 && os.Args[1] == ConvoCommand {
		os.Exit(convoMain(os.Args[2:]))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("설정을 읽지 못했습니다", "err", err)
		os.Exit(1)
	}

	// Every module and what ties them, put together where the conversation's
	// tests put it together too (internal/assemble).
	agent, err := assemble.Assemble(&cfg, assemble.Deps{Log: log})
	if err != nil {
		log.Error("에이전트를 세우지 못했습니다", "err", err)
		os.Exit(1)
	}
	loops, stopLoops := context.WithCancel(context.Background())
	defer stopLoops()
	if err := agent.Start(loops); err != nil {
		log.Error("에이전트를 시작하지 못했습니다", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: agent.API.Handler(),
		// No WriteTimeout: /events streams and /mcp blocks on a human.
		ReadHeaderTimeout: 10 * time.Second,
	}

	restored, _ := agent.Thread.Snapshot()

	go func() {
		log.Info("jarvis agent listening",
			"addr", cfg.Addr,
			"now", time.Now().Format("2006-01-02 15:04 MST"),
			"workspace", cfg.Workspace,
			"modules", agent.Modules.Names(),
			"turns", len(restored),
			"proposals", len(agent.Proposals.List()),
			"permission_tool", permission.QualifiedToolName(),
			"auto_allowed", cfg.AllowedTools,
			"builtin_tools", builtinTools(cfg.BuiltinTools),
			"cache_ttl", cfg.CacheTTL,
			"session_idle", cfg.SessionIdle.String(),
			"policy_servers", agent.Policy.Servers,
			"policy_builtins", agent.Policy.Builtins)
		if len(cfg.DroppedTools) > 0 {
			log.Warn("JARVIS_ALLOWED_TOOLS 에서 카드를 건너뛰면 안 되는 도구를 뺐습니다", "dropped", cfg.DroppedTools)
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("서버가 죽었습니다", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	agent.Stop(ctx)
	log.Info("종료했습니다")
}

// builtinTools is the --tools value for the log: "default" when unset.
func builtinTools(p *string) string {
	if p == nil {
		return "default"
	}
	return *p
}
