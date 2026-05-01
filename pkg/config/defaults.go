// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package config

import (
	"encoding/json"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg"
)

// DefaultConfig returns the default configuration for PicoClaw.
func DefaultConfig() *Config {
	workspacePath := filepath.Join(GetHome(), pkg.WorkspaceName)

	return &Config{
		Version: CurrentVersion,
		// Isolation is opt-in so existing installations keep their current behavior
		// until the user explicitly enables subprocess sandboxing.
		Isolation: IsolationConfig{
			Enabled: false,
		},
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:                 workspacePath,
				RestrictToWorkspace:       true,
				Provider:                  "",
				MaxTokens:                 32768,
				Temperature:               nil, // nil means use provider default
				MaxToolIterations:         50,
				SummarizeMessageThreshold: 20,
				SummarizeTokenPercent:     75,
				SteeringMode:              "one-at-a-time",
				ToolFeedback: ToolFeedbackConfig{
					Enabled:          false,
					MaxArgsLength:    300,
					SeparateMessages: false,
				},
				SplitOnMarker: false,
				// PIKA-V3 defaults:
				MemoryDBPath:           filepath.Join(workspacePath, "memory", "bot_memory.db"),
				BaseToolsDir:           filepath.Join(workspacePath, "tools"),
				SkillsDir:              filepath.Join(workspacePath, "skills"),
				MaxToolsInPrompt:       40,
				TelemetryEnabled:       true,
				TelemetryRetentionDays: 30,
				MaxRetriesPerMessage:   3,
				ToolCallRetryEnabled:   true,
				LoopDetectionThreshold: 3,
				IdleTimeoutMin:         30,
				ContextManager:         "pika",
			},
			List: []AgentConfig{
				{ID: "main", Default: true, Name: "\u041f\u0438\u043a\u0430"},
			},
		},
		Session: SessionConfig{
			Dimensions: []string{"chat"},
		},
		Channels: defaultChannels(),
		Hooks: HooksConfig{
			Enabled: true,
			Defaults: HookDefaultsConfig{
				ObserverTimeoutMS:    500,
				InterceptorTimeoutMS: 5000,
				ApprovalTimeoutMS:    60000,
			},
		},
		ModelList: []*ModelConfig{
			// ============================================
			// Add your API key to the model you want to use
			// ============================================

			// Zhipu AI
			{
				ModelName: "glm-4.7",
				Provider:  "zhipu",
				Model:     "glm-4.7",
				APIBase:   "https://open.bigmodel.cn/api/paas/v4",
			},
			// OpenAI
			{
				ModelName: "gpt-5.4",
				Provider:  "openai",
				Model:     "gpt-5.4",
				APIBase:   "https://api.openai.com/v1",
			},
			// Anthropic Claude
			{
				ModelName: "claude-sonnet-4.6",
				Provider:  "anthropic",
				Model:     "claude-sonnet-4.6",
				APIBase:   "https://api.anthropic.com/v1",
			},
			// DeepSeek
			{
				ModelName: "deepseek-chat",
				Provider:  "deepseek",
				Model:     "deepseek-chat",
				APIBase:   "https://api.deepseek.com/v1",
			},
			// Venice AI
			{
				ModelName: "venice-uncensored",
				Provider:  "venice",
				Model:     "venice-uncensored",
				APIBase:   "https://api.venice.ai/api/v1",
			},
			// Google Gemini
			{
				ModelName: "gemini-2.0-flash",
				Provider:  "gemini",
				Model:     "gemini-2.0-flash-exp",
				APIBase:   "https://generativelanguage.googleapis.com/v1beta",
			},
			// Qwen
			{
				ModelName: "qwen-plus",
				Provider:  "qwen",
				Model:     "qwen-plus",
				APIBase:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
			},
			// Moonshot
			{
				ModelName: "moonshot-v1-8k",
				Provider:  "moonshot",
				Model:     "moonshot-v1-8k",
				APIBase:   "https://api.moonshot.cn/v1",
			},
			// Groq
			{
				ModelName: "llama-3.3-70b",
				Provider:  "groq",
				Model:     "llama-3.3-70b-versatile",
				APIBase:   "https://api.groq.com/openai/v1",
			},
			// OpenRouter
			{
				ModelName: "openrouter-auto",
				Provider:  "openrouter",
				Model:     "auto",
				APIBase:   "https://openrouter.ai/api/v1",
			},
			{
				ModelName: "openrouter-gpt-5.4",
				Provider:  "openrouter",
				Model:     "openai/gpt-5.4",
				APIBase:   "https://openrouter.ai/api/v1",
			},
			// NVIDIA
			{
				ModelName: "nemotron-4-340b",
				Provider:  "nvidia",
				Model:     "nemotron-4-340b-instruct",
				APIBase:   "https://integrate.api.nvidia.com/v1",
			},
			// Cerebras
			{
				ModelName: "cerebras-llama-3.3-70b",
				Provider:  "cerebras",
				Model:     "llama-3.3-70b",
				APIBase:   "https://api.cerebras.ai/v1",
			},
			// Vivgrid
			{
				ModelName: "vivgrid-auto",
				Provider:  "vivgrid",
				Model:     "auto",
				APIBase:   "https://api.vivgrid.com/v1",
			},
			// Volcengine
			{
				ModelName: "ark-code-latest",
				Provider:  "volcengine",
				Model:     "ark-code-latest",
				APIBase:   "https://ark.cn-beijing.volces.com/api/v3",
			},
			{
				ModelName: "doubao-pro",
				Provider:  "volcengine",
				Model:     "doubao-pro-32k",
				APIBase:   "https://ark.cn-beijing.volces.com/api/v3",
			},
			// ShengsuanYun
			{
				ModelName: "deepseek-v3",
				Provider:  "shengsuanyun",
				Model:     "deepseek-v3",
				APIBase:   "https://api.shengsuanyun.com/v1",
			},
			// Antigravity
			{
				ModelName:  "gemini-flash",
				Provider:   "antigravity",
				Model:      "gemini-3-flash",
				AuthMethod: "oauth",
			},
			// GitHub Copilot
			{
				ModelName:  "copilot-gpt-5.4",
				Provider:   "github-copilot",
				Model:      "gpt-5.4",
				APIBase:    "http://localhost:4321",
				AuthMethod: "oauth",
			},
			// Ollama
			{
				ModelName: "llama3",
				Provider:  "ollama",
				Model:     "llama3",
				APIBase:   "http://localhost:11434/v1",
			},
			// Mistral AI
			{
				ModelName: "mistral-small",
				Provider:  "mistral",
				Model:     "mistral-small-latest",
				APIBase:   "https://api.mistral.ai/v1",
			},
			// Avian
			{
				ModelName: "deepseek-v3.2",
				Provider:  "avian",
				Model:     "deepseek/deepseek-v3.2",
				APIBase:   "https://api.avian.io/v1",
			},
			{
				ModelName: "kimi-k2.5",
				Provider:  "avian",
				Model:     "moonshotai/kimi-k2.5",
				APIBase:   "https://api.avian.io/v1",
			},
			// Minimax
			{
				ModelName: "MiniMax-M2.5",
				Provider:  "minimax",
				Model:     "MiniMax-M2.5",
				APIBase:   "https://api.minimaxi.com/v1",
				ExtraBody: map[string]any{"reasoning_split": true},
			},
			// LongCat
			{
				ModelName: "LongCat-Flash-Thinking",
				Provider:  "longcat",
				Model:     "LongCat-Flash-Thinking",
				APIBase:   "https://api.longcat.chat/openai",
			},
			// ModelScope
			{
				ModelName: "modelscope-qwen",
				Provider:  "modelscope",
				Model:     "Qwen/Qwen3-235B-A22B-Instruct-2507",
				APIBase:   "https://api-inference.modelscope.cn/v1",
			},
			// VLLM
			{
				ModelName: "local-model",
				Provider:  "vllm",
				Model:     "custom-model",
				APIBase:   "http://localhost:8000/v1",
			},
			// LM Studio
			{
				ModelName: "lmstudio-local",
				Provider:  "lmstudio",
				Model:     "openai/gpt-oss-20b",
				APIBase:   "http://localhost:1234/v1",
			},
			// Azure OpenAI
			{
				ModelName: "azure-gpt5",
				Provider:  "azure",
				Model:     "my-gpt5-deployment",
				APIBase:   "https://your-resource.openai.azure.com",
			},
		},
		Gateway: GatewayConfig{
			Host:      "localhost",
			Port:      18790,
			HotReload: false,
			LogLevel:  DefaultGatewayLogLevel,
		},
		Tools: ToolsConfig{
			FilterSensitiveData: true,
			FilterMinLength:     8,
			MediaCleanup: MediaCleanupConfig{
				ToolConfig: ToolConfig{Enabled: true},
				MaxAge:   30,
				Interval: 5,
			},
			Web: WebToolsConfig{
				ToolConfig:      ToolConfig{Enabled: true},
				Provider:        "auto",
				PreferNative:    true,
				Proxy:           "",
				FetchLimitBytes: 10 * 1024 * 1024,
				Format:          "plaintext",
				Brave:       BraveConfig{Enabled: false, MaxResults: 5},
				Tavily:      TavilyConfig{Enabled: false, MaxResults: 5},
				Sogou:       SogouConfig{Enabled: true, MaxResults: 5},
				DuckDuckGo:  DuckDuckGoConfig{Enabled: false, MaxResults: 5},
				Perplexity:  PerplexityConfig{Enabled: false, MaxResults: 5},
				SearXNG:     SearXNGConfig{Enabled: false, BaseURL: "", MaxResults: 5},
				GLMSearch:   GLMSearchConfig{Enabled: false, BaseURL: "https://open.bigmodel.cn/api/paas/v4/web_search", SearchEngine: "search_std", MaxResults: 5},
				BaiduSearch: BaiduSearchConfig{Enabled: false, BaseURL: "https://qianfan.baidubce.com/v2/ai_search/web_search", MaxResults: 10},
			},
			Cron: CronToolsConfig{
				ToolConfig:         ToolConfig{Enabled: true},
				ExecTimeoutMinutes: 5,
				AllowCommand:       true,
			},
			Exec: ExecConfig{
				ToolConfig:         ToolConfig{Enabled: true},
				EnableDenyPatterns: true,
				AllowRemote:        true,
				TimeoutSeconds:     60,
			},
			Skills: SkillsToolsConfig{
				ToolConfig: ToolConfig{Enabled: true},
				Registries: SkillsRegistriesConfig{
					&SkillRegistryConfig{Name: "clawhub", Enabled: true, BaseURL: "https://clawhub.ai", Param: map[string]any{}},
					&SkillRegistryConfig{Name: "github", Enabled: true, BaseURL: "https://github.com", Param: map[string]any{}},
				},
				MaxConcurrentSearches: 2,
				SearchCache: SearchCacheConfig{MaxSize: 50, TTLSeconds: 300},
			},
			SendFile:    ToolConfig{Enabled: true},
			SendTTS:     ToolConfig{Enabled: false},
			MCP: MCPConfig{
				ToolConfig: ToolConfig{Enabled: false},
				Discovery: ToolDiscoveryConfig{Enabled: false, TTL: 5, MaxSearchResults: 5, UseBM25: true, UseRegex: false},
				MaxInlineTextChars: DefaultMCPMaxInlineTextChars,
				Servers:            map[string]MCPServerConfig{},
			},
			AppendFile:   ToolConfig{Enabled: true},
			EditFile:     ToolConfig{Enabled: true},
			FindSkills:   ToolConfig{Enabled: true},
			I2C:          ToolConfig{Enabled: false},
			InstallSkill: ToolConfig{Enabled: true},
			ListDir:      ToolConfig{Enabled: true},
			Message:      ToolConfig{Enabled: true},
			ReadFile:     ReadFileToolConfig{Enabled: true, Mode: ReadFileModeBytes, MaxReadFileSize: 64 * 1024},
			Serial:       ToolConfig{Enabled: false},
			Spawn:        ToolConfig{Enabled: true},
			SpawnStatus:  ToolConfig{Enabled: false},
			SPI:          ToolConfig{Enabled: false},
			Subagent:     ToolConfig{Enabled: true},
			WebFetch:     ToolConfig{Enabled: true},
			WriteFile:    ToolConfig{Enabled: true},
			// PIKA-V3: BASE tool master switch
			BaseTools: BaseToolsConfig{
				Enabled: true, Exec: true, ReadFile: true,
				WriteFile: true, EditFile: true, AppendFile: true, ListDir: true,
			},
		},
		Heartbeat: HeartbeatConfig{Enabled: true, Interval: 30},
		Devices:   DevicesConfig{Enabled: false, MonitorUSB: true},
		Voice: VoiceConfig{
			ModelName:         "",
			TTSModelName:      "",
			EchoTranscription: false,
			ElevenLabsAPIKey:  "",
		},
		BuildInfo: BuildInfo{
			Version:   Version,
			GitCommit: GitCommit,
			BuildTime: BuildTime,
			GoVersion: GoVersion,
		},
		// PIKA-V3: cross-agent configs
		Clarify: ClarifyConfig{
			Enabled: true, TimeoutMin: 30,
			MaxStreakBeforeBypass: 2, PrecheckTimeoutMs: 3000,
		},
		Security: SecurityConfig{
			DangerousOps: DangerousOpsConfig{ConfirmTimeoutMin: 30},
			RAD: RADConfig{Enabled: true, DriftThreshold: 0.2, BlockScore: 3, WarnScore: 2},
			MCP: MCPSecurityConfig{
				TaintResetPolicy: "explicit_only", StdioUser: "mcp-sandbox",
				StdioIsolation: "user", PerServerRPM: 60,
				DefaultCapabilities: map[string]bool{
					"sampling": false, "roots": false, "elicitation": false,
				},
				DefaultAllowResources: true,
			},
		},
		Health: HealthConfig{
			WindowSize: 5, ToolFailThresholdPct: 30, LatencyThresholdMs: 30000,
			FallbackProvider: FallbackProviderConfig{
				Provider: "stepfun", APIKeyEnv: "STEPFUN_API_KEY", Model: "step-3.5-flash",
			},
			Reporting: HealthReportingConfig{
				TypingIndicatorEnabled: true, AlertDedupPerSession: true,
				DailyHealthSummaryEnabled: true,
			},
			Progress: ProgressConfig{
				Enabled: true, ThrottleSec: 2, DeleteOnComplete: true,
				ShowStepText: true, StopCommandEnabled: true,
			},
		},
	}
}

func defaultChannels() ChannelsConfig {
	defs := map[string]any{
		"whatsapp": map[string]any{
			"settings": map[string]any{
				"bridge_url": "ws://localhost:3001",
			},
		},
		"telegram": map[string]any{
			"typing":      map[string]any{"enabled": true},
			"placeholder": map[string]any{"enabled": true, "text": []string{"Thinking... \ud83d\udcad"}},
			"settings": map[string]any{
				"streaming":       map[string]any{"enabled": true, "throttle_seconds": 3, "min_growth_chars": 200},
				"use_markdown_v2": false,
			},
		},
		"feishu":  map[string]any{},
		"discord": map[string]any{},
		"maixcam": map[string]any{
			"settings": map[string]any{"host": "0.0.0.0", "port": 18790},
		},
		"qq": map[string]any{
			"settings": map[string]any{"max_message_length": 2000},
		},
		"dingtalk": map[string]any{},
		"slack":    map[string]any{},
		"matrix": map[string]any{
			"group_trigger": map[string]any{"mention_only": true},
			"placeholder":   map[string]any{"enabled": true, "text": []string{"Thinking... \ud83d\udcad"}},
			"settings": map[string]any{
				"homeserver":     "https://matrix.org",
				"join_on_invite": true,
			},
		},
		"line": map[string]any{
			"group_trigger": map[string]any{"mention_only": true},
			"settings": map[string]any{
				"webhook_host": "0.0.0.0",
				"webhook_port": 18791,
				"webhook_path": "/webhook/line",
			},
		},
		"onebot": map[string]any{
			"settings": map[string]any{
				"ws_url":             "ws://127.0.0.1:3001",
				"reconnect_interval": 5,
			},
		},
		"wecom": map[string]any{
			"settings": map[string]any{
				"websocket_url":         "wss://openws.work.weixin.qq.com",
				"send_thinking_message": true,
			},
		},
		"weixin": map[string]any{
			"settings": map[string]any{
				"base_url":     "https://ilinkai.weixin.qq.com/",
				"cdn_base_url": "https://novac2c.cdn.weixin.qq.com/c2c",
			},
		},
		"pico": map[string]any{
			"settings": map[string]any{
				"ping_interval":   30,
				"read_timeout":    60,
				"write_timeout":   10,
				"max_connections": 100,
			},
		},
		"irc": map[string]any{
			"settings": map[string]any{
				"server":   "",
				"tls":      true,
				"nick":     "picoclaw",
				"channels": []string{},
			},
		},
	}

	channels := make(ChannelsConfig, len(defs))
	for name, def := range defs {
		data, err := json.Marshal(def)
		if err != nil {
			continue
		}
		bc := &Channel{}
		if err := json.Unmarshal(data, bc); err != nil {
			continue
		}
		bc.SetName(name)
		if bc.Type == "" {
			bc.Type = name
		}
		channels[name] = bc
	}
	return channels
}
