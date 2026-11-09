package judge

import "regexp"

const redacted = "[REDACTED]"

// redactions rewrite credentials in a command before it is persisted. Each keeps the text around
// the secret so the audit line stays readable. Order matters: structured forms first.
var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), redacted},
	{regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://)[^\s/@"']+@`), "${1}" + redacted + "@"},
	{regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?(?:(?:bearer|basic|token)\s+)?)[^\s"']+`), "${1}" + redacted},
	{regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}${2}" + redacted},
	{regexp.MustCompile(`((?:^|\s)(?:-u|--user)(?:\s+|=)["']?)[^\s"':]+:[^\s"']+`), "${1}" + redacted},
	{regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|token|secret|api[_-]?key)[a-z0-9_.-]*)(["']?\s*[:=]\s*["']?)[^\s"'&;$][^\s"'&;]*`), "${1}${2}" + redacted},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|hf_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|xox[abprs]-[A-Za-z0-9-]{10,})`), redacted},
}

// Redact replaces the credentials it recognises in s with [REDACTED].
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}
