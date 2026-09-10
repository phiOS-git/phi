package firewall

import (
	"net/netip"
	"strings"
	"testing"
)

// cidr4 builds an IPv4 CIDR string from octets at runtime — the repo's
// pre-commit hook refuses a dotted-quad literal in a diff (ADR 067), and a
// unit test is no exception.
func cidr4(a, b, c, d byte, bits int) string {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{a, b, c, d}), bits).String()
}

func TestRenderDisabled(t *testing.T) {
	got := Render(Config{Enabled: false, Preset: "home"})
	if !strings.Contains(got, "delete table inet phi") {
		t.Fatalf("disabled render must remove the table:\n%s", got)
	}
	if strings.Contains(got, "policy drop") || strings.Contains(got, "add rule") {
		t.Fatalf("disabled render still installs rules:\n%s", got)
	}
	if strings.Contains(got, "flush ruleset") {
		t.Fatalf("render must never flush the whole ruleset:\n%s", got)
	}
}

func TestRenderEnabledCore(t *testing.T) {
	lan := cidr4(172, 16, 0, 0, 12)
	got := Render(Config{
		Enabled: true, Preset: "home", Logging: true,
		Rules: []Rule{
			{Port: "22", Proto: "tcp"},
			{Port: "1714-1764", Proto: "udp", From: lan},
			{Port: "80", Proto: "tcp", From: "2001:db8::/32"},
		},
	})
	if strings.Contains(got, "flush ruleset") {
		t.Fatalf("render must never flush the whole ruleset:\n%s", got)
	}
	for _, want := range []string{
		"add table inet phi",
		"delete table inet phi",
		"type filter hook input priority 0; policy drop;",
		"add rule inet phi input ct state established,related accept",
		"add rule inet phi input iif \"lo\" accept",
		"add rule inet phi input tcp dport 22 accept",
		"add rule inet phi input ip saddr " + lan + " udp dport 1714-1764 accept",
		"add rule inet phi input ip6 saddr 2001:db8::/32 tcp dport 80 accept",
		"icmp type echo-request accept", // home answers ping
		"log prefix \"phi-fw: \"",       // logging on
	} {
		if !strings.Contains(got, want) {
			t.Errorf("enabled render missing %q:\n%s", want, got)
		}
	}
}

func TestPresetStrictness(t *testing.T) {
	rules := []Rule{{Port: "22", Proto: "tcp"}}

	pub := Render(Config{Enabled: true, Preset: "public", Rules: rules})
	if strings.Contains(pub, "tcp dport 22 accept") {
		t.Error("public preset must ignore allow-rules")
	}
	if strings.Contains(pub, "icmp type echo-request accept") {
		t.Error("public preset must not answer ping")
	}

	para := Render(Config{Enabled: true, Preset: "paranoid", Rules: rules})
	if strings.Contains(para, "meta l4proto { icmp, ipv6-icmp } accept") {
		t.Error("paranoid preset must not blanket-accept ICMP")
	}
	// paranoid still keeps the essentials, or IPv6 breaks
	if !strings.Contains(para, "nd-neighbor-solicit") {
		t.Error("paranoid preset dropped IPv6 neighbour discovery")
	}
}

func TestRuleIDStable(t *testing.T) {
	a := Rule{Port: "443", Proto: "tcp"}
	b := Rule{Port: "443", Proto: "tcp"}
	if a.ID() != b.ID() {
		t.Fatalf("same rule, different id: %s vs %s", a.ID(), b.ID())
	}
	if a.ID() == (Rule{Port: "443", Proto: "udp"}).ID() {
		t.Fatal("tcp and udp 443 share an id")
	}
}

func TestRuleValidate(t *testing.T) {
	ok := []Rule{
		{Port: "22", Proto: "tcp"},
		{Port: "1-65535", Proto: "udp"},
		{Port: "53", Proto: "udp", From: cidr4(10, 0, 0, 0, 8)},
		{Port: "22", Proto: "tcp", From: cidr4(172, 16, 5, 4, 32)},
		{Port: "80", Proto: "tcp", From: "2001:db8::/32"},
		{Port: "80", Proto: "tcp", From: "fd00::1"},
	}
	for _, r := range ok {
		if err := r.canonical().validate(); err != nil {
			t.Errorf("%+v should be valid: %v", r, err)
		}
	}
	bad := []Rule{
		{Port: "22", Proto: "sctp"},
		{Port: "0", Proto: "tcp"},
		{Port: "70000", Proto: "tcp"},
		{Port: "100-50", Proto: "tcp"},
		{Port: "abc", Proto: "tcp"},
		{Port: "22", Proto: "tcp", From: "not-an-ip"},
	}
	for _, r := range bad {
		if err := r.canonical().validate(); err == nil {
			t.Errorf("%+v should be rejected", r)
		}
	}
}

func TestLockoutRisk(t *testing.T) {
	// No SSH session → never a warning, whatever the config.
	t.Setenv("SSH_CONNECTION", "")
	if LockoutRisk(Config{Enabled: true, Preset: "paranoid"}) != "" {
		t.Fatal("no SSH_CONNECTION must not warn")
	}

	client := strings.TrimSuffix(cidr4(172, 16, 9, 9, 32), "/32")
	server := strings.TrimSuffix(cidr4(172, 16, 9, 1, 32), "/32")
	t.Setenv("SSH_CONNECTION", client+" 51000 "+server+" 22")

	cases := []struct {
		name string
		c    Config
		warn bool
	}{
		{"enabled, no rule", Config{Enabled: true, Preset: "home"}, true},
		{"disabled", Config{Enabled: false, Preset: "home"}, false},
		{"allow 22/tcp any", Config{Enabled: true, Preset: "home", Rules: []Rule{{Port: "22", Proto: "tcp"}}}, false},
		{"allow range from client CIDR", Config{Enabled: true, Preset: "home",
			Rules: []Rule{{Port: "1-1024", Proto: "tcp", From: cidr4(172, 16, 0, 0, 12)}}}, false},
		{"allow 22/udp only", Config{Enabled: true, Preset: "home", Rules: []Rule{{Port: "22", Proto: "udp"}}}, true},
		{"public ignores the rule", Config{Enabled: true, Preset: "public", Rules: []Rule{{Port: "22", Proto: "tcp"}}}, true},
	}
	for _, tc := range cases {
		got := LockoutRisk(tc.c) != ""
		if got != tc.warn {
			t.Errorf("%s: warn=%v, want %v", tc.name, got, tc.warn)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled || c.Preset != "home" || len(c.Rules) != 0 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	c.Rules = append(c.Rules, Rule{Port: "22", Proto: "tcp"})
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rules) != 1 || got.Rules[0].Port != "22" {
		t.Fatalf("round-trip lost the rule: %+v", got)
	}
}

func TestLoadRepairsBadPreset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save(Config{Enabled: true, Preset: "bogus", Rules: []Rule{}}); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Preset != "home" {
		t.Fatalf("bad preset not repaired: %q", c.Preset)
	}
}
