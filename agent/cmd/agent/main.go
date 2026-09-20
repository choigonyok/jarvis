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
	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/httpapi"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("설정을 읽지 못했습니다", "err", err)
		os.Exit(1)
	}

	// One bus, three producers, one stream out. A proposal raised with no
	// conversation behind it reaches the browser the same way a chat turn does.
	events := bus.New()
	transcript := thread.NewStore(events)
	proposals := proposal.NewStore(events)
	modules := module.NewRegistry()

	// --- 모듈 -------------------------------------------------------------
	// Adding a feature is one Add call. Removing one is deleting the line -
	// nothing below this block names a specific module.
	calendarStore := calendar.NewStore(cfg.CalendarPath, events)
	calendarModule := calendar.New(calendarStore)
	modules.Add(calendarModule)

	mcpMounts := map[string]http.Handler{
		"/mcp": permission.NewGate(
			proposals, modules, transcript, cfg.ApprovalWait, cfg.Debug, log,
		).Handler(),
		"/mcp/" + calendar.ServerName: calendarModule.Handler(),
	}
	cfg.ModuleMCPURLs[calendar.ServerName] = cfg.PublicMCPURL + "/" + calendar.ServerName

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	if err := modules.Start(startCtx); err != nil {
		cancelStart()
		log.Error("모듈을 시작하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cancelStart()

	runner := claudecode.New(cfg, transcript, log)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Deps{
			Thread:        transcript,
			Proposals:     proposals,
			Calendar:      calendarStore,
			Runner:        runner,
			Bus:           events,
			MCP:           mcpMounts,
			AllowedOrigin: cfg.AllowedOrigin,
			Log:           log,
		}).Handler(),
		// No WriteTimeout: /events streams and /mcp blocks on a human.
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("jarvis agent listening",
			"addr", cfg.Addr,
			"now", time.Now().Format("2006-01-02 15:04 MST"),
			"workspace", cfg.Workspace,
			"modules", modules.Names(),
			"permission_tool", permission.QualifiedToolName(),
			"auto_allowed", cfg.AllowedTools)
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
	_ = modules.Stop(ctx)
	log.Info("종료했습니다")
}
