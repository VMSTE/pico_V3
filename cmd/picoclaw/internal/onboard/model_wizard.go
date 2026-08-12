package onboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/sipeed/picoclaw/pkg/config"
)

// D-AUDIT-85: interactive model setup wizard for `picoclaw onboard`.
// Skipped for non-terminal stdin (the launcher runs onboard non-interactively)
// and when a default model is already configured.

type wizardProvider struct {
	Key       string
	Label     string
	APIBase   string
	KeyURL    string // where to get an API key ("" for local servers)
	Anthropic bool   // use x-api-key + anthropic-version instead of Bearer
	NoKey     bool   // local server, no real key needed
}

var wizardProviders = []wizardProvider{
	{
		Key:     "ollama",
		Label:   "Ollama — local, free (requires ollama running)",
		APIBase: "http://localhost:11434/v1",
		NoKey:   true,
	},
	{
		Key:     "openai",
		Label:   "OpenAI",
		APIBase: "https://api.openai.com/v1",
		KeyURL:  "https://platform.openai.com/api-keys",
	},
	{
		Key:       "anthropic",
		Label:     "Anthropic (Claude)",
		APIBase:   "https://api.anthropic.com/v1",
		KeyURL:    "https://console.anthropic.com/settings/keys",
		Anthropic: true,
	},
	{
		Key:     "openrouter",
		Label:   "OpenRouter — 100+ models",
		APIBase: "https://openrouter.ai/api/v1",
		KeyURL:  "https://openrouter.ai/keys",
	},
	{
		Key:     "deepseek",
		Label:   "DeepSeek",
		APIBase: "https://api.deepseek.com/v1",
		KeyURL:  "https://platform.deepseek.com/",
	},
	{
		Key:     "groq",
		Label:   "Groq — fast inference",
		APIBase: "https://api.groq.com/openai/v1",
		KeyURL:  "https://console.groq.com/keys",
	},
}

func maybeRunModelWizard(cfg *config.Config, configPath string, skipModel bool) {
	if skipModel {
		return
	}
	if cfg.Agents.Defaults.ModelName != "" {
		return
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}

	fmt.Println("\nSelect your AI provider:")
	fmt.Println()
	for i, p := range wizardProviders {
		line := fmt.Sprintf("  %d. %s", i+1, p.Label)
		if p.KeyURL != "" {
			line += fmt.Sprintf("  ->  %s", p.KeyURL)
		}
		fmt.Println(line)
	}
	fmt.Printf("  %d. Other (configure manually later)\n", len(wizardProviders)+1)
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	var provider *wizardProvider
	for provider == nil {
		fmt.Printf("Choice [1-%d] (0 to skip): ", len(wizardProviders)+1)
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		if text == "" || text == "0" {
			return
		}
		idx, aErr := strconv.Atoi(text)
		if aErr != nil || idx < 1 || idx > len(wizardProviders)+1 {
			fmt.Println("Invalid choice, try again.")
			continue
		}
		if idx == len(wizardProviders)+1 {
			fmt.Println("OK — add a model later with: picoclaw model add -b <api-base> -k <api-key>")
			return
		}
		provider = &wizardProviders[idx-1]
	}

	apiKey := ""
	if provider.NoKey {
		apiKey = provider.Key // placeholder; local servers ignore auth
	} else {
		for apiKey == "" {
			fmt.Printf("API key (%s): ", provider.KeyURL)
			raw, rErr := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if rErr != nil {
				fmt.Printf("Error reading key: %v\n", rErr)
				return
			}
			apiKey = strings.TrimSpace(string(raw))
			if apiKey == "" {
				fmt.Println("Key must not be empty.")
			}
		}
	}

	fmt.Println("Testing connection...")
	ids, fErr := fetchModelIDs(provider, apiKey)
	if fErr != nil {
		fmt.Printf("Connection failed: %v\n", fErr)
		fmt.Printf(
			"Config saved without a model. Retry later with: picoclaw model add -b %s -k <key>\n",
			provider.APIBase,
		)
		return
	}
	if len(ids) == 0 {
		fmt.Println("The endpoint answered but returned no models.")
		return
	}
	fmt.Printf("Connected — %d model(s) available.\n\n", len(ids))

	modelID := pickWizardModel(reader, ids)

	secureKeys := config.SimpleSecureStrings(apiKey)
	found := false
	for _, m := range cfg.ModelList {
		if m == nil {
			continue
		}
		if m.ModelName == provider.Key {
			m.Model = modelID
			m.APIBase = provider.APIBase
			m.APIKeys = secureKeys
			m.Enabled = true
			found = true
			break
		}
	}
	if !found {
		cfg.ModelList = append(cfg.ModelList, &config.ModelConfig{
			ModelName: provider.Key,
			Model:     modelID,
			APIBase:   provider.APIBase,
			APIKeys:   secureKeys,
			Enabled:   true,
		})
	}
	cfg.Agents.Defaults.ModelName = provider.Key

	if sErr := config.SaveConfig(configPath, cfg); sErr != nil {
		fmt.Printf("Error saving config: %v\n", sErr)
		return
	}
	fmt.Printf("\nModel connected: %s (saved as %q, set as default)\n", modelID, provider.Key)
}

// fetchModelIDs verifies connectivity with a single free GET /models call
// (no generation, no cost) and returns the available model ids.
func fetchModelIDs(p *wizardProvider, apiKey string) ([]string, error) {
	req, rErr := http.NewRequest(http.MethodGet, strings.TrimSuffix(p.APIBase, "/")+"/models", nil)
	if rErr != nil {
		return nil, rErr
	}
	if p.Anthropic {
		req.Header.Set("X-Api-Key", apiKey)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, dErr := client.Do(req)
	if dErr != nil {
		return nil, dErr
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if jErr := json.NewDecoder(resp.Body).Decode(&payload); jErr != nil {
		return nil, fmt.Errorf("decode models list: %w", jErr)
	}
	ids := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

func pickWizardModel(reader *bufio.Reader, ids []string) string {
	const maxShow = 20
	for i, id := range ids {
		if i >= maxShow {
			fmt.Printf("  ... and %d more (type the full id to choose one)\n", len(ids)-maxShow)
			break
		}
		fmt.Printf("  %2d) %s\n", i+1, id)
	}
	for {
		fmt.Print("Pick a model (number or id): ")
		text, _ := reader.ReadString('\n')
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if idx, aErr := strconv.Atoi(text); aErr == nil {
			if idx >= 1 && idx <= len(ids) && idx <= maxShow {
				return ids[idx-1]
			}
			fmt.Println("Out of range; try again.")
			continue
		}
		for _, id := range ids {
			if id == text {
				return id
			}
		}
		fmt.Println("Not a valid number or model id; try again.")
	}
}
