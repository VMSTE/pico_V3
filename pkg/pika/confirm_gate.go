// PIKA-V3: confirm_gate.go — ConfirmGate builtin hook (ToolApprover, D-136a).
// Р-5: гейт по эффекту — разбор того, что вызов ДЕЛАЕТ, а не имени
// инструмента (D-AUDIT-51/52). Exec-команда разбирается на сегменты,
// каждый классифицируется; нераспознанное спрашивается (deny-by-default
// к операциям). Fail-closed: timeout → deny, error → deny.
//
// NOTE: pkg/pika cannot import pkg/agent (import cycle via
// context_pika.go). Approval types are defined locally here.
//
// TelegramSender interface is defined in progress.go (shared across
// progress, clarify, confirm_gate). This file uses it without
// re-declaring.

package pika

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ConfirmApprovalRequest is a local mirror of agent.ToolApprovalRequest.
type ConfirmApprovalRequest struct {
	Tool      string
	Arguments map[string]any
}

// ConfirmApprovalDecision is a local mirror of agent.ApprovalDecision.
type ConfirmApprovalDecision struct {
	Approved bool
	Reason   string
}

// ConfirmGate is a builtin ToolApprover hook (D-136a).
//
// Decision flow (Р-5):
//  0. Empty ops table → gate disarmed, allow everything (back-compat)
//  1. deriveEffects: exec command → segments → effect per segment;
//     file write tools → files.write; legacy tool.operation key kept
//  2. Reflex: compose.restart + exited → allow (container recovery)
//  3. Per effect: rule from ops table (always / if_healthy /
//     if_critical_path / never); armed gate + unknown strict effect → ask
//  4. if_healthy + degraded → allow + NOTIFY manager (D-AUDIT-52)
//  5. Confirmation needed → SendConfirmation to manager, wait for reply
//  6. Timeout or error → deny (fail-closed)
type ConfirmGate struct {
	ops           map[string]config.DangerousOpEntry
	criticalPaths []string
	timeoutMin    int
	sender        TelegramSender
	healthGetter  SystemStateProvider
}

// ConfirmGateFactory creates a new ConfirmGate from config.
// Config path: hooks.builtins.pika.confirm_gate.enabled: true.
func ConfirmGateFactory(
	cfg *config.Config,
	sender TelegramSender,
	health SystemStateProvider,
) *ConfirmGate {
	ops := make(map[string]config.DangerousOpEntry, len(cfg.Security.DangerousOps.Ops))
	for key, entry := range cfg.Security.DangerousOps.Ops {
		ops[key] = entry
	}

	timeoutMin := cfg.Security.DangerousOps.ConfirmTimeoutMin
	if timeoutMin <= 0 {
		timeoutMin = 30 // default 30 min
	}

	return &ConfirmGate{
		ops:           ops,
		criticalPaths: cfg.Security.DangerousOps.CriticalPaths,
		timeoutMin:    timeoutMin,
		sender:        sender,
		healthGetter:  health,
	}
}

// opEffect — распознанный эффект вызова инструмента (Р-5).
type opEffect struct {
	Key    string // ключ в таблице dangerous_ops
	Detail string // человекочитаемое описание для сообщения менеджеру
	Path   string // целевой путь для files.write (если есть)
	// Strict: эффект от детектора команд/файлов — вооружённый гейт
	// спрашивает даже без строки в таблице (deny-by-default к операциям).
	// false — legacy-ключ tool.operation: отсутствие в таблице = allow
	// (обратная совместимость).
	Strict bool
}

// ApproveTool evaluates whether a tool call requires confirmation
// and, if so, requests it from the manager.
// Returns (decision, nil) in all cases — errors from the sender
// are converted to deny decisions (fail-closed).
func (cg *ConfirmGate) ApproveTool(
	ctx context.Context,
	req *ConfirmApprovalRequest,
) (ConfirmApprovalDecision, error) {
	approved := ConfirmApprovalDecision{Approved: true}

	// 0. Гейт без таблицы операций разоружён (обратная совместимость:
	// до Р-5 пустая таблица означала «всё разрешено»).
	if len(cg.ops) == 0 {
		return approved, nil
	}

	// 1. Эффекты вызова (детектор команд и файловых инструментов).
	effects := deriveEffects(req.Tool, req.Arguments)

	// 1b. Legacy-ключ tool.operation. Рефлекс compose.restart+exited
	// (аварийное восстановление контейнера) сохранён.
	if op := getOperation(req.Arguments); op != "" {
		legacy := opEffect{
			Key:    req.Tool + "." + op,
			Detail: summarizeArgs(req.Arguments),
		}
		if legacy.Key == "compose.restart" && isExited(req.Arguments) {
			logger.InfoCF("confirm_gate",
				"reflex: compose.restart + exited → allow", nil)
		} else {
			effects = append(effects, legacy)
		}
	}

	if len(effects) == 0 {
		return approved, nil
	}

	// 2. Оцениваем каждый эффект отдельно.
	var needConfirm []opEffect
	healthBypassed := false
	for _, eff := range effects {
		entry, inTable := cg.ops[eff.Key]
		if !inTable {
			// Вооружённый гейт + неизвестный рискованный эффект →
			// спрашиваем (deny-by-default к операциям, D-AUDIT-51).
			if eff.Strict {
				needConfirm = append(needConfirm, eff)
			}
			continue
		}
		need, bypassed := cg.evaluateConfirmRule(entry, eff, req.Arguments)
		if bypassed {
			healthBypassed = true
		}
		if need {
			needConfirm = append(needConfirm, eff)
		}
	}

	// 3. Ничего не требует подтверждения → allow.
	if len(needConfirm) == 0 {
		if healthBypassed {
			cg.notifyHealthBypass(effects)
		}
		return approved, nil
	}

	// 4. Guard: sender unavailable → deny (fail-closed)
	if cg.sender == nil {
		return ConfirmApprovalDecision{
			Approved: false,
			Reason:   "confirmation error: sender unavailable",
		}, nil
	}

	// 5. Спрашиваем менеджера (живой диалог через SendConfirmation).
	msg := fmt.Sprintf(
		"🔴 Подтвердите опасные действия (ответьте да/нет, %d мин):\n%s",
		cg.timeoutMin, formatEffects(needConfirm),
	)

	timeout := time.Duration(cg.timeoutMin) * time.Minute
	confirmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := cg.sender.SendConfirmation(confirmCtx, msg)
	if err != nil {
		logger.ErrorCF("confirm_gate",
			fmt.Sprintf("confirmation error: %v", err), nil)
		return ConfirmApprovalDecision{
			Approved: false,
			Reason:   "confirmation error: " + err.Error(),
		}, nil
	}

	if !result {
		return ConfirmApprovalDecision{
			Approved: false,
			Reason:   "менеджер отклонил",
		}, nil
	}

	return approved, nil
}

// evaluateConfirmRule определяет, нужно ли подтверждение для эффекта.
// bypassedByHealth=true: if_healthy разрешил действие из-за деградации
// системы — вызывающий код обязан уведомить менеджера (D-AUDIT-52).
func (cg *ConfirmGate) evaluateConfirmRule(
	op config.DangerousOpEntry,
	eff opEffect,
	args map[string]any,
) (needConfirm bool, bypassedByHealth bool) {
	switch op.Confirm {
	case config.ConfirmAlways:
		return true, false

	case config.ConfirmNever:
		return false, false

	case config.ConfirmIfHealthy:
		if cg.healthGetter == nil {
			// No health provider → confirm for safety
			return true, false
		}
		state := cg.healthGetter.GetSystemState()
		if state.Status == StateHealthy.Status {
			return true, false
		}
		// degraded/offline → allow (emergency fix), но с уведомлением
		logger.InfoCF("confirm_gate",
			fmt.Sprintf("if_healthy: system %s → allow without confirmation",
				state.Status), nil)
		return false, true

	case config.ConfirmIfCritical:
		target := eff.Path
		if target == "" {
			target = extractPath(args)
		}
		if !isInCriticalPath(target, cg.criticalPaths) {
			logger.InfoCF("confirm_gate",
				"if_critical_path: path not in critical_paths → allow", nil)
			return false, false
		}
		return true, false

	default:
		// Unknown confirm mode → confirm for safety
		logger.WarnCF("confirm_gate",
			fmt.Sprintf("unknown confirm mode %q → confirming for safety",
				string(op.Confirm)), nil)
		return true, false
	}
}

// notifyHealthBypass — D-AUDIT-52: аварийное восстановление не ждёт
// подтверждения, но менеджер обязан узнать. Fire-and-forget с коротким
// таймаутом, чтобы не блокировать гейт.
func (cg *ConfirmGate) notifyHealthBypass(effects []opEffect) {
	if cg.sender == nil {
		return
	}
	state := "unknown"
	if cg.healthGetter != nil {
		state = cg.healthGetter.GetSystemState().Status
	}
	msg := fmt.Sprintf(
		"⚠️ Система %s — выполняю восстановление без подтверждения:\n%s",
		state, formatEffects(effects),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cg.sender.SendMessage(ctx, msg); err != nil {
		logger.WarnCF("confirm_gate",
			"health-bypass notification failed",
			map[string]any{"error": err.Error()})
	}
}

// ---------------------------------------------------------------------------
// Р-5: детектор эффектов
// ---------------------------------------------------------------------------

// deriveEffects распознаёт эффекты вызова инструмента.
// Пустой список — read-only операции и инструменты без эффектов.
func deriveEffects(tool string, args map[string]any) []opEffect {
	switch tool {
	case "write_file", "edit_file", "append_file":
		p := extractPath(args)
		return []opEffect{{
			Key: "files.write", Detail: tool + " " + p, Path: p, Strict: true,
		}}
	case "exec":
		action, _ := args["action"].(string)
		// list/poll/read/write/kill/send-keys управляют фоновыми
		// сессиями, запущенными самим агентом; мутирует только run.
		if action != "" && action != "run" {
			return nil
		}
		cmd, _ := args["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			return nil
		}
		return classifyCommand(cmd)
	}
	return nil
}

// classifyCommand разбирает exec-команду на сегменты и классифицирует
// каждый. Сцепленные команды (; && || |) не проскакивают мимо —
// критерий готовности Р-5.
func classifyCommand(cmd string) []opEffect {
	segments := splitShellChain(cmd)
	effects := make([]opEffect, 0, len(segments))
	for _, seg := range segments {
		effects = append(effects, classifySegment(seg)...)
	}
	return dedupEffects(effects)
}

// splitShellChain делит команду на сегменты по ; && || | & с учётом
// кавычек. Подстановка $(...)/бэктики/незакрытая кавычка → вся строка
// одним сегментом (уйдёт в exec.unknown → спросим; deny-гард shell.go
// такие команды к тому же блокирует).
func splitShellChain(cmd string) []string {
	var segments []string
	var cur strings.Builder
	var quote rune // 0, '\'', '"'
	runes := []rune(cmd)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			cur.WriteRune(r)
		case '$':
			if i+1 < len(runes) && runes[i+1] == '(' {
				return []string{cmd}
			}
			cur.WriteRune(r)
		case '`':
			return []string{cmd}
		case ';', '&':
			flush()
			if i+1 < len(runes) && runes[i+1] == r {
				i++ // && и %% — двойной символ
			}
		case '|':
			flush()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return []string{cmd}
	}
	flush()
	return segments
}

// classifySegment классифицирует один сегмент команды.
func classifySegment(seg string) []opEffect {
	fields := stripEnvPrefixes(strings.Fields(seg))
	if len(fields) == 0 {
		return nil
	}

	var effects []opEffect
	// Редирект вывода в файл — это запись, добавляем к эффектам команды.
	if target := redirectTarget(seg); target != "" &&
		!safeRedirectTargets[target] {
		effects = append(effects, opEffect{
			Key: "files.write", Detail: seg + " → " + target,
			Path: target, Strict: true,
		})
	}

	base := baseCommand(fields[0])
	switch base {
	case "docker":
		if idx := indexOfSubcommand(fields[1:], "compose", dockerValueFlags); idx >= 0 {
			return append(effects, composeEffect(fields[1+idx+1:], seg)...)
		}
		sub := firstNonFlag(fields[1:], dockerValueFlags)
		if readOnlyDockerSub[sub] {
			return effects
		}
		return append(effects, unknownEffect(seg)...)
	case "docker-compose":
		return append(effects, composeEffect(fields[1:], seg)...)
	case "systemctl":
		switch firstNonFlag(fields[1:], nil) {
		case "restart", "reload", "start":
			return append(effects, opEffect{
				Key: "service.restart", Detail: seg, Strict: true,
			})
		case "status", "is-active", "is-enabled", "is-failed",
			"show", "cat", "list-units", "list-timers":
			return effects
		}
		return append(effects, unknownEffect(seg)...)
	case "git":
		sub := firstNonFlag(fields[1:], gitValueFlags)
		if sub == "push" {
			return append(effects, opEffect{
				Key: "git.push", Detail: seg, Strict: true,
			})
		}
		if readOnlyGitSub[sub] {
			return effects
		}
		return append(effects, unknownEffect(seg)...)
	case "curl", "wget":
		return append(effects, httpEffect(fields[1:], seg)...)
	case "scp", "rsync", "nc", "ncat", "sftp":
		return append(effects, opEffect{
			Key: "data.exfil", Detail: seg, Strict: true,
		})
	case "find":
		for _, f := range fields[1:] {
			if f == "-delete" || f == "-exec" || f == "-execdir" {
				return append(effects, unknownEffect(seg)...)
			}
		}
		return effects
	case "sed":
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-i") {
				return append(effects, unknownEffect(seg)...)
			}
		}
		return effects
	}

	if readOnlyCommands[base] {
		return effects
	}
	return append(effects, unknownEffect(seg)...)
}

// composeEffect классифицирует подкоманду docker compose.
func composeEffect(args []string, seg string) []opEffect {
	switch firstNonFlag(args, composeValueFlags) {
	case "up":
		return []opEffect{{Key: "compose.up", Detail: seg, Strict: true}}
	case "down":
		return []opEffect{{Key: "compose.down", Detail: seg, Strict: true}}
	case "restart":
		return []opEffect{{Key: "compose.restart", Detail: seg, Strict: true}}
	case "ps", "logs", "config", "images", "top", "ls",
		"version", "stats", "events", "inspect", "":
		return nil
	}
	return unknownEffect(seg)
}

// httpEffect классифицирует curl/wget: выгрузка файла наружу →
// data.exfil (D-AUDIT-52: «передать базу — с запросом»), сохранение
// ответа в файл → files.write. GET и inline-данные — чтение.
func httpEffect(args []string, seg string) []opEffect {
	var effects []opEffect
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inlineVal := a, ""
		if idx := strings.Index(a, "="); strings.HasPrefix(a, "--") && idx > 0 {
			name, inlineVal = a[:idx], a[idx+1:]
		}
		if exfilFlags[name] {
			effects = append(effects, opEffect{
				Key: "data.exfil", Detail: seg, Strict: true,
			})
			continue
		}
		if dataFlags[name] {
			val := inlineVal
			if val == "" && i+1 < len(args) {
				val = args[i+1]
			}
			if strings.HasPrefix(val, "@") {
				// -d @file — отправка содержимого файла наружу
				effects = append(effects, opEffect{
					Key: "data.exfil", Detail: seg, Strict: true,
				})
			}
			continue
		}
		if outputFlags[name] {
			val := inlineVal
			if val == "" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				val = args[i+1]
			}
			effects = append(effects, opEffect{
				Key: "files.write", Detail: seg + " → " + val,
				Path: val, Strict: true,
			})
		}
	}
	return effects
}

// --- таблицы классификации ---

var exfilFlags = map[string]bool{
	"-F": true, "--form": true, "--form-string": true,
	"-T": true, "--upload-file": true, "--post-file": true,
}

var dataFlags = map[string]bool{
	"-d": true, "--data": true, "--data-binary": true,
	"--data-raw": true, "--data-ascii": true,
}

var outputFlags = map[string]bool{
	"-o": true, "--output": true, "-O": true, "--remote-name": true,
}

var composeValueFlags = map[string]bool{
	"-f": true, "--file": true, "-p": true, "--project-name": true,
	"--env-file": true, "--profile": true, "--project-directory": true,
}

var gitValueFlags = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true,
	"--work-tree": true, "--namespace": true,
}

var dockerValueFlags = map[string]bool{
	"--context": true, "-H": true, "--host": true, "--config": true,
}

var readOnlyDockerSub = map[string]bool{
	"ps": true, "logs": true, "images": true, "inspect": true,
	"stats": true, "version": true, "info": true, "top": true,
	"port": true, "diff": true, "ls": true, "search": true,
}

var readOnlyGitSub = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true,
	"branch": true, "fetch": true, "describe": true, "rev-parse": true,
	"rev-list": true, "blame": true, "ls-files": true, "grep": true,
	"shortlog": true, "reflog": true, "whatchanged": true,
	"count-objects": true, "ls-remote": true,
}

// readOnlyCommands — команды-чтение, не требующие подтверждения.
// Осознанно НЕ входят: env/printenv (утечка секретов в контекст),
// awk/perl/python (Turing-complete), xargs (запускает произвольное).
var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "pwd": true,
	"grep": true, "wc": true, "ps": true, "df": true, "du": true,
	"free": true, "uname": true, "whoami": true, "hostname": true,
	"date": true, "id": true, "which": true, "type": true,
	"echo": true, "printf": true, "true": true, "false": true,
	"test": true, "sleep": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "stat": true, "file": true,
	"diff": true, "sort": true, "uniq": true, "tr": true, "cut": true,
	"jq": true, "yq": true, "ip": true, "ss": true, "netstat": true,
	"lsof": true, "dig": true, "nslookup": true, "host": true,
	"cd": true, "exit": true, "umask": true, "ping": true,
}

// safeRedirectTargets — псевдоустройства, редирект в которые не запись.
var safeRedirectTargets = map[string]bool{
	"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true,
}

// --- helpers ---

// unknownEffect — нераспознанный потенциально мутирующий сегмент.
func unknownEffect(seg string) []opEffect {
	return []opEffect{{Key: "exec.unknown", Detail: seg, Strict: true}}
}

// baseCommand отсекает путь до бинаря (/usr/bin/git → git).
func baseCommand(token string) string {
	if idx := strings.LastIndex(token, "/"); idx >= 0 {
		return token[idx+1:]
	}
	return token
}

// stripEnvPrefixes снимает присваивания окружения в начале команды
// (FOO=bar cmd) — иначе base command не распознаётся (обход через
// env-префикс, индустриальный антипаттерн claude-code#66176).
func stripEnvPrefixes(fields []string) []string {
	for len(fields) > 0 {
		f := fields[0]
		eq := strings.Index(f, "=")
		if eq <= 0 || strings.HasPrefix(f, "-") {
			break
		}
		valid := true
		for _, r := range f[:eq] {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
				r >= '0' && r <= '9' || r == '_') {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		fields = fields[1:]
	}
	return fields
}

// firstNonFlag возвращает первый позиционный (не-флаг) токен,
// пропуская значения флагов со значением (git -C /path push → push).
func firstNonFlag(args []string, valueFlags map[string]bool) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if valueFlags[a] {
			i++ // пропускаем значение флага
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// indexOfSubcommand ищет первый позиционный токен; возвращает его
// индекс, если он равен sub, иначе -1.
func indexOfSubcommand(args []string, sub string, valueFlags map[string]bool) int {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if valueFlags[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == sub {
			return i
		}
		return -1
	}
	return -1
}

// redirectTarget возвращает путь перенаправления вывода (>, >>),
// игнорируя перенаправления дескрипторов (2>&1) и кавычки.
func redirectTarget(seg string) string {
	runes := []rune(seg)
	var quote rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '>' {
			rest := strings.TrimLeft(string(runes[i+1:]), "> \t")
			if strings.HasPrefix(rest, "&") {
				return "" // 2>&1 и т.п. — не запись в файл
			}
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return strings.Trim(fields[0], "\"'")
			}
			return ""
		}
	}
	return ""
}

// dedupEffects убирает повторы (одинаковые ключ+описание).
func dedupEffects(in []opEffect) []opEffect {
	seen := make(map[string]bool, len(in))
	var out []opEffect
	for _, e := range in {
		k := e.Key + "\x00" + e.Detail
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

// formatEffects — список эффектов для сообщения менеджеру.
func formatEffects(effects []opEffect) string {
	var sb strings.Builder
	for _, eff := range effects {
		sb.WriteString("• ")
		sb.WriteString(eff.Key)
		if eff.Detail != "" {
			d := eff.Detail
			if len(d) > 120 {
				d = d[:117] + "..."
			}
			sb.WriteString(": ")
			sb.WriteString(d)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// --- legacy helpers (до Р-5) ---

// getOperation extracts the "operation" field from tool arguments.
func getOperation(args map[string]any) string {
	if args == nil {
		return ""
	}
	if op, ok := args["operation"]; ok {
		if s, ok := op.(string); ok {
			return s
		}
	}
	return ""
}

// isExited checks if the container state in arguments is "exited".
// Used for the compose.restart reflex: exited container → allow restart
// without confirmation (recovery scenario).
func isExited(args map[string]any) bool {
	if args == nil {
		return false
	}
	if state, ok := args["state"]; ok {
		if s, ok := state.(string); ok {
			return s == "exited"
		}
	}
	return false
}

// isInCriticalPath checks whether target matches any critical_paths glob.
// NOTE (Р-5): поверхностное сравнение — укрепление (filepath.Clean +
// рекурсивные **) это шаг 3, отдельной веткой.
func isInCriticalPath(target string, criticalPaths []string) bool {
	if target == "" || len(criticalPaths) == 0 {
		return false
	}
	for _, pattern := range criticalPaths {
		if matched, err := filepath.Match(pattern, target); err == nil && matched {
			return true
		}
	}
	return false
}

// extractPath extracts a file path from tool arguments.
func extractPath(args map[string]any) string {
	for _, key := range []string{"path", "file"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// summarizeArgs creates a short human-readable summary of tool arguments
// for the confirmation message.
func summarizeArgs(args map[string]any) string {
	if args == nil || len(args) == 0 {
		return "no args"
	}

	var parts []string
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}

	summary := strings.Join(parts, ", ")
	if len(summary) > 100 {
		summary = summary[:97] + "..."
	}
	return summary
}
