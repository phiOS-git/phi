package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// SSHHostsProvider lists hosts named in ~/.ssh/config's own `Host`
// directives (S-33 AGENT: "known SSH hosts"). Wildcard patterns
// ("Host *", "Host *.example.com") are skipped — they are not a real,
// connectable host by themselves, only a config-matching pattern.
type SSHHostsProvider struct{}

func (SSHHostsProvider) Name() string { return "ssh" }

func (p SSHHostsProvider) Query(_ context.Context, q string) []Result {
	if q == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	var results []Result
	for _, h := range parseSSHConfigHosts(string(data)) {
		results = append(results, Result{
			ID: "ssh:" + h, Provider: p.Name(),
			Title: h, Subtitle: "SSH host",
			Action: Action{Kind: ActionExecTerminal, Data: map[string]string{"command": "ssh " + h}},
		})
	}
	return results
}

func parseSSHConfigHosts(content string) []string {
	var hosts []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 5 || !strings.EqualFold(line[:5], "host ") {
			continue
		}
		for _, h := range strings.Fields(line)[1:] {
			if strings.ContainsAny(h, "*?") {
				continue
			}
			hosts = append(hosts, h)
		}
	}
	return hosts
}
