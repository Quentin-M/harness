package transcript

import "strings"

// Injected harness context that reaches a transcript as user turns.
var noisePrefixes = []string{
	"<system-reminder>",
	"<task-notification>",
	"<local-command",
	"<command-",
	"<teammate-message",
	"<user-prompt-submit-hook",
	"<environment_context",
	"<bash-",
	"# AGENTS.md instructions",
	"Caveat:",
	"[Request interrupted by user",
	"Another Claude session sent a message",
	"This session is being continued from a previous conversation",
}

// noise reports whether a user turn was written by a harness rather than a person.
func noise(text string) bool {
	for _, p := range noisePrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// clip cuts s to at most n bytes on a rune boundary, marking the cut.
func clip(s string, n int) string {
	const mark = "…"
	if len(s) <= n {
		return s
	}
	if n < len(mark) {
		return ""
	}
	cut := n - len(mark)
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + mark
}
