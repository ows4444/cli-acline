package redact

import (
	"strings"
	"testing"
)

func TestSecretsRedactsKnownFormats(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		leaks []string // substrings that must NOT survive in the output
	}{
		{"github pat", "found the bug, token is ghp_abcdefghijklmnopqrstuvwxyz0123456789 fyi", []string{"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"slack token", "posted with xoxb-1234567890-abcdefghijklmnop", []string{"xoxb-1234567890-abcdefghijklmnop"}},
		{"stripe key", "using sk_live_51H8x9zABCDEFGHIJKLMN for the demo", []string{"sk_live_51H8x9zABCDEFGHIJKLMN"}},
		{"aws access key id", "id is AKIAIOSFODNN7EXAMPLE for the shared bucket", []string{"AKIAIOSFODNN7EXAMPLE"}},
		{"google api key", "AIzaSyD-9tSrke72PouQMnMX-a7eZSW0jkFMBWY leaked in a commit", []string{"AIzaSyD-9tSrke72PouQMnMX-a7eZSW0jkFMBWY"}},
		{"private key block", "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----", []string{"MIIBOgIBAAJBAK"}},
		{"jwt", "auth header was Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", []string{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"}},
		{"generic assignment", "API_KEY=sk_test_51H8x9zABCDEFGHIJKLMN in the .env I pasted", []string{"sk_test_51H8x9zABCDEFGHIJKLMN"}},
		{"postgres connection string", "connect with postgres://appuser:hunter2pass@db.internal:5432/prod", []string{"hunter2pass"}},
		{"mongodb+srv connection string", "using mongodb+srv://admin:s3cr3tValue@cluster0.mongodb.net/mydb", []string{"s3cr3tValue"}},
		{"anthropic key", "key is sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcdefgh ok", []string{"sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcdefgh"}},
		{"openai project key", "sk-proj-Abcdefghijklmnopqrstuvwxyz_0123456789-AbCd here", []string{"sk-proj-Abcdefghijklmnopqrstuvwxyz_0123456789-AbCd"}},
		{"bearer token", "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456' x", []string{"abcdefghijklmnopqrstuvwxyz123456"}},
		{"voyage key", "VOYAGE is pa-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AB used", []string{"pa-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AB"}},
		{"npm token", "npm_abcdefghijklmnopqrstuvwxyz0123456789 in .npmrc", []string{"npm_abcdefghijklmnopqrstuvwxyz0123456789"}},
		{"gitlab pat", "glpat-abcdefghijklmnopqrst was pasted", []string{"glpat-abcdefghijklmnopqrst"}},
		{"huggingface token", "hf_abcdefghijklmnopqrstuvwxyzABCDEFGH leaked", []string{"hf_abcdefghijklmnopqrstuvwxyzABCDEFGH"}},
		{"redis connection string with empty username", "cache is at redis://:onlyThisPassword123@cache.internal:6379/0", []string{"onlyThisPassword123"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, found := Secrets(c.text)
			if !found {
				t.Fatalf("expected a secret to be found in %q", c.text)
			}
			if !strings.Contains(out, "[REDACTED]") {
				t.Errorf("expected output to contain [REDACTED], got %q", out)
			}
			for _, leak := range c.leaks {
				if strings.Contains(out, leak) {
					t.Errorf("secret value %q leaked into output %q", leak, out)
				}
			}
		})
	}
}

func TestSecretsConnectionStringRedactsOnlyThePassword(t *testing.T) {
	out, found := Secrets("connect with postgres://appuser:hunter2pass@db.internal:5432/prod")
	if !found {
		t.Fatal("expected the connection string password to be found")
	}
	for _, want := range []string{"postgres://appuser:", "@db.internal:5432/prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected scheme/user/host to survive redaction, got %q (missing %q)", out, want)
		}
	}
}

func TestSecretsLeavesOrdinaryTextAlone(t *testing.T) {
	safe := []string{
		"fixed the bug in guard.go where rm -rf wasn't detected",
		"decided to split dangerous vs routine bash patterns",
		"the api design still needs work before we ship",
		"see postgres://localhost:5432/mydb for the local dev connection",
	}
	for _, s := range safe {
		out, found := Secrets(s)
		if found {
			t.Errorf("expected %q to be left alone, but it was flagged and became %q", s, out)
		}
		if out != s {
			t.Errorf("expected %q unchanged, got %q", s, out)
		}
	}
}

func TestSecretsBearerKeepsTheWordButRedactsTheToken(t *testing.T) {
	out, _ := Secrets("Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456")
	if !strings.Contains(out, "Bearer [REDACTED]") {
		t.Errorf("got %q, want the scheme kept and only the token redacted", out)
	}
}

func TestSecretsDoesNotTouchOrdinaryProse(t *testing.T) {
	for _, text := range []string{
		"the bearer of bad news arrived", "task-list is the pa-rameter", "sk-ip this step", "npm install left-pad",
		"hf_ is a prefix", "we chose a pa-ttern here",
	} {
		if out, found := Secrets(text); found || out != text {
			t.Errorf("Secrets(%q) = %q, %v; want unchanged", text, out, found)
		}
	}
}

func TestFieldsRedactsInPlaceAndReportsIfAnyHadASecret(t *testing.T) {
	a, b, c := "clean text", "key sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcdefgh here", ""
	var nilField *string
	if !Fields(&a, &b, &c, nilField) {
		t.Fatal("Fields should report that a secret was found")
	}
	if a != "clean text" || c != "" {
		t.Errorf("untouched fields changed: %q %q", a, c)
	}
	if strings.Contains(b, "sk-ant-") || !strings.Contains(b, "[REDACTED]") {
		t.Errorf("secret not redacted in place: %q", b)
	}
	if Fields(&a, &c) {
		t.Error("Fields reported a secret in clean text")
	}
}

// acline's own approval token was not recognised, so one pasted into a
// note or a check detail was stored and later shown to agents.
func TestSecretsRedactsTheApprovalToken(t *testing.T) {
	tok := "acl_" + strings.Repeat("0123456789abcdef", 4)
	got, found := Secrets("the token is " + tok + " ok")
	if !found || strings.Contains(got, tok) || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("Secrets = %q, %v", got, found)
	}
	if _, found := Secrets("acl_short and acl_ are fine"); found {
		t.Error("a non-token acl_ prefix was redacted")
	}
}
