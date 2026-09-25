package cmd

import "testing"

// Credential files and irreversible commands the guard used to let through
// (reproduced with `acline guard check-tool`).

func TestGuardBlocksMoreCredentialFiles(t *testing.T) {
	for _, path := range []string{
		"/home/u/.kube/config", "/home/u/.docker/config.json", "/home/u/.git-credentials", "/home/u/.pgpass",
		"/srv/infra/terraform.tfstate", "/srv/infra/terraform.tfstate.backup", "/home/u/.ssh/id_ecdsa", "/home/u/.ssh/id_ecdsa.pub",
		"/home/u/keys/AuthKey_ABC123.p8", "/home/u/.config/gcloud/application_default_credentials.json",
		"/home/u/.config/gcloud/credentials.db", "/home/u/.azure/accessTokens.json", "/repo/kubeconfig",
	} {
		if blocked, _ := checkFilePathForSecrets(path); !blocked {
			t.Errorf("Read of %q was allowed", path)
		}
		if blocked, _ := checkBashCommand("cat " + path); !blocked {
			t.Errorf("`cat %s` was allowed", path)
		}
	}
	for _, ok := range []string{"/repo/docs/kube-notes.md", "/repo/internal/state.go", "/repo/main.tf", "/repo/keys.go"} {
		if blocked, _ := checkFilePathForSecrets(ok); blocked {
			t.Errorf("harmless path %q was blocked", ok)
		}
	}
}

func TestGuardBlocksIrreversibleGitAndFindCommands(t *testing.T) {
	blocked := []string{
		"git push --force origin main", "git push -f", "git push origin +main", "git push --force-with-lease origin main",
		"git -C /repo push --force", "cd x && git push origin main --force", "git reset --hard HEAD~5", "git reset --hard",
		"git clean -fd", "git clean -f", "git clean -xfd", "git clean -fdx",
		"find . -delete", "find /repo -name '*.go' -delete", "find . -type f -exec rm {} +",
	}
	for _, c := range blocked {
		if b, _ := checkBashCommand(c); !b {
			t.Errorf("expected BLOCK for %q", c)
		}
	}
	allowed := []string{
		"git push origin main", "git push -u origin feature", "git reset HEAD file.go", "git reset --soft HEAD~1",
		"git clean -n", "git clean --dry-run -d", "find . -name '*.go'", "find . -type f -print",
		`git commit -m "never git push --force or git reset --hard"`,
		`echo "find . -delete is dangerous"`, "git status", "git diff",
	}
	for _, c := range allowed {
		if b, why := checkBashCommand(c); b {
			t.Errorf("expected ALLOW for %q, got %q", c, why)
		}
	}
}

func TestChmodRecursiveIsLoggedNotBlocked(t *testing.T) {
	if b, _ := checkBashCommand("chmod -R 755 build/"); b {
		t.Error("chmod -R is routine; it should be logged, not blocked")
	}
	if checkBashCommandWarnings("chmod -R 755 build/") == "" {
		t.Error("chmod -R should be recorded as a guard_warned event")
	}
}
