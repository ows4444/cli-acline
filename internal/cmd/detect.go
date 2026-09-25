package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// detectProjectName guesses a project name from things already lying around
// the repo, in order of how deliberately a human chose them: the Go module
// path, package.json's name field, the git remote, and finally the directory
// name as a last resort. Used both as the non-interactive default and as the
// suggested answer when `acline init` prompts interactively.
func detectProjectName() string {
	if name, ok := projectNameFromGoMod(); ok {
		return name
	}
	if name, ok := projectNameFromPackageJSON(); ok {
		return name
	}
	if name, ok := projectNameFromGitRemote(); ok {
		return name
	}
	return defaultProjectName()
}

func projectNameFromGoMod() (string, bool) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		mod := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module")), `"`)
		if mod == "" {
			return "", false
		}
		parts := strings.Split(mod, "/")
		name := parts[len(parts)-1]
		return name, name != ""
	}
	return "", false
}

func projectNameFromPackageJSON() (string, bool) {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return "", false
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", false
	}
	name := strings.TrimSpace(pkg.Name)
	// Scoped packages ("@scope/name") should surface just the name part.
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name, name != ""
}

func projectNameFromGitRemote() (string, bool) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", false
	}
	url := strings.TrimSpace(string(out))
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	idx := strings.LastIndex(url, "/")
	if idx < 0 {
		return "", false
	}
	name := url[idx+1:]
	return name, name != ""
}
