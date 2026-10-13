package transcript

import "strings"

// Injected harness context and OpenRig automation that reach a transcript as user turns.
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
	"# Profile: ",
	"Caveat:",
	"[Request interrupted by user",
	"[OpenRig watchdog",
	"Another Claude session sent a message",
	"This session is being continued from a previous conversation",
}

// noise reports whether a user turn was written by a harness or the rig rather than a person.
func noise(text string) bool {
	for _, p := range noisePrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	first, rest, _ := strings.Cut(text, "\n")
	switch {
	case strings.HasPrefix(first, "From: ") && strings.HasPrefix(rest, "To: "):
		return true // rig send, queue handoff notices included
	case strings.HasPrefix(rest, "OpenRig session identity") && strings.Contains(first, "@"):
		return true
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
