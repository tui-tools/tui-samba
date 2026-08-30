package main

import (
	"strings"
	"testing"

	"github.com/tui-tools/tui-kit/compat"
)

// TestRunReportDemo checks the half of the block this tool owns. The kit's own
// tests cover the machine facts and the scrubbing; what has to be right here is
// that --demo says demo, that the imitated suite is named beside it rather than
// the "samba" the fake answers Name() with, and that no server was read to
// produce any of it.
func TestRunReportDemo(t *testing.T) {
	var out strings.Builder
	opts := options{demo: true, report: true}
	if err := runReport(baseConfig(), opts, &out); err != nil {
		t.Fatalf("runReport: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"backend: demo\n",
		"mode: demo (sample data, the system was not read)\n",
		"demo backend: samba\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, toolName+" ") {
		t.Errorf("report should start with the tool name:\n%s", got)
	}
	// The connection reader is a fact about the real suite, and there is none
	// behind a fake.
	if strings.Contains(got, "connections:") {
		t.Errorf("a demo report should claim no connection reader:\n%s", got)
	}
}

// TestRunReportLive renders the live block on whatever machine the tests run
// on — with Samba or without it — and holds it to the promise the form makes
// about it: it always answers, and it never names the user or the machine.
func TestRunReportLive(t *testing.T) {
	t.Setenv("HOSTNAME", "workstation")
	t.Setenv("USER", "alice")
	t.Setenv("HOME", "/home/alice")

	var out strings.Builder
	if err := runReport(baseConfig(), options{report: true}, &out); err != nil {
		t.Fatalf("runReport: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"backend: samba", "mode: live\n", "smb.conf: as the server reports it\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"alice", "workstation", "/home/"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the report leaked %q:\n%s", forbidden, got)
		}
	}
}

// TestConfigLine covers the one configuration value the block reports without
// spelling out: a path the user chose may sit under a home directory, so what
// is published is that it was set, never what it was set to.
func TestConfigLine(t *testing.T) {
	cfg := baseConfig()
	if got, want := configLine(cfg), "as the server reports it"; got != want {
		t.Errorf("configLine = %q, want %q", got, want)
	}

	cfg.Set(keyConfig, "/home/alice/smb.conf")
	got := configLine(cfg)
	if got != "set by configuration" {
		t.Errorf("configLine = %q, want %q", got, "set by configuration")
	}
	if strings.Contains(got, "alice") {
		t.Errorf("configLine published the path: %q", got)
	}
}

// TestConnectionsLine separates the two readers a Samba version decides
// between, which is the difference behind most of what a session row can get
// wrong.
func TestConnectionsLine(t *testing.T) {
	features := []compat.Feature{{Name: statusJSONFeature, Since: "4.17"}}
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"a version with the JSON reader", "4.20.5", "smbstatus --json"},
		{"one below it", "4.16.0", "smbstatus text output (--json needs 4.17)"},
		{"an unreadable version is assumed capable", "", "smbstatus --json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caps := compat.NewCaps(tc.version, features)
			if got := connectionsLine(caps); got != tc.want {
				t.Errorf("connectionsLine(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}
