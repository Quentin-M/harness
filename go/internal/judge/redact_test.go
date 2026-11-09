package judge_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Quentin-M/harness/internal/judge"
	"github.com/Quentin-M/harness/internal/judge/judgetest"
)

func TestRedact(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\nabcd\n-----END RSA PRIVATE KEY-----"
	for _, c := range []struct{ name, in, want string }{
		{"bearer header", `curl -H "Authorization: Bearer abc123DEF456ghi" https://x.io`, `curl -H "Authorization: Bearer [REDACTED]" https://x.io`},
		{"basic header", `curl -H 'Authorization: Basic dXNlcjpwYXNz' x`, `curl -H 'Authorization: Basic [REDACTED]' x`},
		{"bare bearer", `echo Bearer abcdefghijkl1234`, `echo Bearer [REDACTED]`},
		{"curl -u", `curl -u alice:s3cr3tpw https://x.io/a`, `curl -u [REDACTED] https://x.io/a`},
		{"curl --user", `curl --user=alice:s3cr3tpw x`, `curl --user=[REDACTED] x`},
		{"export token", `export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789 && gh api /user`, `export GITHUB_TOKEN=[REDACTED] && gh api /user`},
		{"github literal", `echo ghp_abcdefghijklmnopqrstuvwxyz0123`, `echo [REDACTED]`},
		{"gho literal", `echo gho_abcdefghijklmnopqrstuvwxyz0123`, `echo [REDACTED]`},
		{"fine-grained pat", `git clone https://github_pat_11ABCDEFG0abcdefghijklmnop_xyz1234567890/r`, `git clone https://[REDACTED]/r`},
		{"hf", `echo hf_abcdefghijklmnopqrstuvwxyzABCD`, `echo [REDACTED]`},
		{"sk", `echo sk-proj-abcdefghijklmnopqrstuvwx`, `echo [REDACTED]`},
		{"aws", `echo AKIAIOSFODNN7EXAMPLE`, `echo [REDACTED]`},
		{"slack", `echo xoxb-123456789012-abcdefghij`, `echo [REDACTED]`},
		{"password assignment", `mysql --password=hunter2 -e "select 1"`, `mysql --password=[REDACTED] -e "select 1"`},
		{"python assignment", `requests.get(u, headers={"api_key": "abc123xyz"})`, `requests.get(u, headers={"api_key": "[REDACTED]"})`},
		{"secret spaced", `secret = "topsecretvalue"`, `secret = "[REDACTED]"`},
		{"url userinfo", `git clone https://bob:pw123@git.example.com/r.git`, `git clone https://[REDACTED]@git.example.com/r.git`},
		{"pem block", "k = '''" + pem + "'''\nprint(k)", "k = '''" + redacted + "'''\nprint(k)"},
		{"variable reference kept", `curl -H "X: $TOKEN" --data token=$TOKEN x`, `curl -H "X: $TOKEN" --data token=$TOKEN x`},
		{"benign", `git push -u origin main && go test ./... && echo "tokenize the input"`, `git push -u origin main && go test ./... && echo "tokenize the input"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := judge.Redact(c.in); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

const redacted = "[REDACTED]"

// Credentials in a bash command and in eval code reach the judge intact but never the audit log.
func TestGateAuditNeverPersistsSecrets(t *testing.T) {
	f := judgetest.New(t)
	secrets := []string{"ghp_abcdefghijklmnopqrstuvwxyz0123456789", "s3cr3tpw", "hunter2hunter2", "hf_abcdefghijklmnopqrstuvwxyzABCD"}
	// an old, world-readable log is tightened
	if err := os.MkdirAll(strings.TrimSuffix(judge.AuditPath(f.State), "/decisions.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(judge.AuditPath(f.State), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	serve(t, newGate(f), bashReq(`curl -u alice:s3cr3tpw -H "Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz0123456789" https://x.io`))
	if !bytes.Contains(f.Server.LastDecision(), []byte("s3cr3tpw")) {
		t.Fatal("the judge must see the command unredacted")
	}
	serve(t, newGate(f), evalReq("import os\nos.environ['HF_TOKEN'] = 'hf_abcdefghijklmnopqrstuvwxyzABCD'\npassword = \"hunter2hunter2\"\n", "python"))
	data, err := os.ReadFile(judge.AuditPath(f.State))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if bytes.Contains(data, []byte(s)) {
			t.Errorf("audit log holds %q:\n%s", s, data)
		}
	}
	if strings.Count(string(data), "\n") != 2 || !bytes.Contains(data, []byte(redacted)) {
		t.Fatalf("audit log:\n%s", data)
	}
	if fi, _ := os.Stat(judge.AuditPath(f.State)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("audit log mode %v", fi.Mode().Perm())
	}
}
