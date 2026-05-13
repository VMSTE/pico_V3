// pkg/pika/onboarding.go
// PIKA-V3: Onboarding detection + prompt injection (ТЗ-v2-10c Block C).
// When USER.md contains placeholder markers, injects onboarding instructions
// into system prompt. After agent writes USER.md via files.write, cache
// invalidates (mtime change) and onboarding addon disappears.

package pika

import "strings"

// OnboardingPlaceholder is the sentinel marker in USER.md that indicates
// the file has not been personalized yet.
const OnboardingPlaceholder = "(заполняется при onboard)"

// NeedsOnboarding returns true if the USER.md content still contains
// onboarding placeholders.
func NeedsOnboarding(userContent string) bool {
	return strings.Contains(userContent, OnboardingPlaceholder)
}

// OnboardingPromptAddon returns the system prompt block that instructs
// the agent to conduct the introductory dialog on first launch.
func OnboardingPromptAddon() string {
	return `## 🤝 ONBOARDING (first launch detected)

USER.md contains placeholder markers — this user has not completed setup.
On the VERY FIRST message from the user, start a brief introductory dialog.

Detect language from the user's first message and respond in that language.

1. Greet briefly and introduce yourself as AtoMinD.
2. Ask these questions (naturally, not as a form):
   - Name
   - What do you do? (dev, design, management, other)
   - Communication style preference (brief / detailed / contextual)
3. After getting answers, use files.write to update workspace/USER.md:
   - Replace every "(заполняется при onboard)" with the real value
   - Replace "(определяется при onboard)" with detected OS from environment context
   - Keep the file structure intact — only fill in the blanks
4. Confirm briefly that setup is complete.

RULES:
- If the user's first message is a direct task or question, greet briefly
  and combine the greeting with the answer. Do not block work for onboarding.
- After files.write succeeds, the placeholder markers disappear from USER.md.
  The prompt cache invalidates on next turn and this instruction will not
  appear again — onboarding is complete.`
}
