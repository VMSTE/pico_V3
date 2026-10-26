// Package reflector — PIKA-V3: ручной запуск Рефлексора из CLI (волна 124).
// Точка входа к существующему ReflectorPipeline.Run — та же сборка,
// что и у `picoclaw agent`: config → provider → AgentLoop → GetReflector.
// Зачем: отладка памяти и боевые прогоны без ожидания cron
// (daily 03:00 / weekly / monthly).
package reflector

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// runTimeout — верхний предел одного ручного прогона (monthly full scan
// по большой базе + LLM-ретрай). Cron-путь таймаута не имеет; для ручной
// команды страхуемся от зависшего провайдера.
const runTimeout = 10 * time.Minute

// NewReflectorCommand — `picoclaw reflector`.
func NewReflectorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reflector",
		Short: "PIKA-V3 Reflector — ручной прогон анализа knowledge_atoms",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <daily|weekly|monthly>",
		Short: "Запустить Reflector pipeline в указанном режиме",
		Long: "Запускает Reflector pipeline против боевой базы памяти.\n" +
			"Режимы: daily (атомы за сутки), weekly (7 дней), " +
			"monthly (полный скан + кристаллизация).\n" +
			"Перед боевым прогоном сделайте бэкап БД " +
			"(sqlite3 .backup) — merge дублей необратим без копии.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{pika.ReflectorDaily, pika.ReflectorWeekly, pika.ReflectorMonthly},
		RunE: func(_ *cobra.Command, args []string) error {
			return runReflector(args[0])
		},
	})
	return cmd
}

func runReflector(mode string) error {
	switch mode {
	case pika.ReflectorDaily, pika.ReflectorWeekly, pika.ReflectorMonthly:
	default:
		return fmt.Errorf("unknown mode %q (want daily|weekly|monthly)", mode)
	}

	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.ConfigureFromEnv()

	provider, modelID, err := providers.CreateProvider(cfg)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	agentLoop := agent.NewAgentLoop(cfg, msgBus, provider)
	defer agentLoop.Close()

	r := agentLoop.GetReflector()
	if r == nil {
		return fmt.Errorf("reflector is not wired (pika memory disabled in config?)")
	}

	fmt.Printf("%s Reflector run: mode=%s (timeout %s)\n", internal.Logo, mode, runTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	if err := r.Run(ctx, mode); err != nil {
		return fmt.Errorf("reflector run: %w", err)
	}
	fmt.Printf("%s Reflector run OK: mode=%s\n", internal.Logo, mode)
	return nil
}
