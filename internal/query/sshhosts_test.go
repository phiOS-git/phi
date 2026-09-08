package query

import "testing"

func TestParseSSHConfigHosts(t *testing.T) {
	content := `# comment
Host razer
    HostName razer.local
    User flavio

Host mini
    User flavio

Host *
    ServerAliveInterval 60

host zotac
    User flavio
`
	hosts := parseSSHConfigHosts(content)
	want := map[string]bool{"razer": true, "mini": true, "zotac": true}
	if len(hosts) != len(want) {
		t.Fatalf("parseSSHConfigHosts = %v, want exactly %v (wildcard excluded)", hosts, want)
	}
	for _, h := range hosts {
		if !want[h] {
			t.Errorf("unexpected host %q", h)
		}
		if h == "*" {
			t.Error("wildcard Host * must not be returned")
		}
	}
}

func TestParseSSHConfigHostsMultipleOnOneLine(t *testing.T) {
	hosts := parseSSHConfigHosts("Host razer razer-vpn\n  User flavio\n")
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts from one Host line, got %v", hosts)
	}
}

func TestParseSSHConfigHostsEmpty(t *testing.T) {
	if hosts := parseSSHConfigHosts(""); hosts != nil {
		t.Errorf("expected nil for empty config, got %v", hosts)
	}
}
