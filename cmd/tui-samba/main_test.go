package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/config"
	"github.com/tui-tools/tui-samba/internal/samba"
)

// baseConfig is the configuration as it stands before the flags are folded in.
func baseConfig() config.Config {
	return config.Config{Tool: toolName, Values: defaults()}
}

// devNull is a writer for the flag package that a test does not want to see.
func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestParseFlags(t *testing.T) {
	opts, err := parseFlags([]string{"--demo", "--theme", "/t/colors.toml",
		"--config", "/srv/smb.conf"}, devNull(t))
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !opts.demo || opts.themePath != "/t/colors.toml" ||
		opts.configPath != "/srv/smb.conf" {
		t.Errorf("opts = %+v", opts)
	}
	if opts.sudoSet {
		t.Error("sudoSet should be false when -sudo is absent")
	}
}

func TestApplyOverrides(t *testing.T) {
	cfg := baseConfig()
	applyOverrides(&cfg, options{themePath: "/t/colors.toml"})
	if got := cfg.Theme(); got != "/t/colors.toml" {
		t.Errorf("Theme() = %q", got)
	}
	// An untouched -sudo must not clear the configured prefix.
	if got := cfg.String(config.KeySudo, ""); got != "sudo -n" {
		t.Errorf("sudo = %q, want the config value", got)
	}

	// An explicit empty -sudo disables escalation.
	cfg = baseConfig()
	applyOverrides(&cfg, options{sudoSet: true, sudo: ""})
	if got := cfg.String(config.KeySudo, "unset"); got != "" {
		t.Errorf("sudo = %q, want empty", got)
	}
	if got := cfg.SudoPrefix(); got != nil {
		t.Errorf("SudoPrefix = %q, want nil", got)
	}
}

func TestDefaultsCoverEveryFlag(t *testing.T) {
	// Every key a flag can override must be declared, otherwise the
	// environment layer silently skips it.
	for _, key := range []string{config.KeySudo, config.KeyTheme, keyConfig} {
		if _, ok := defaults()[key]; !ok {
			t.Errorf("defaults() is missing %q", key)
		}
	}
}

func TestPickBackendDemo(t *testing.T) {
	backend, err := pickBackend(baseConfig(), options{demo: true},
		compat.Result{}.Caps())
	if err != nil {
		t.Fatalf("pickBackend: %v", err)
	}
	if !strings.Contains(backend.Describe(), "demo") {
		t.Errorf("Describe = %q, want it to say it is a demo", backend.Describe())
	}
}

// TestCheckReportsTheState covers the contract the smoke test depends on: the
// counts, the shares and the accounts a shell script can grep for.
func TestCheckReportsTheState(t *testing.T) {
	backend := samba.NewFake()
	var out bytes.Buffer
	if err := runCheck(backend, compat.Result{}, &out); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	for _, want := range []string{
		`"tool": "tui-samba"`,
		`"backend": "samba"`,
		`"installed": true`,
		// The sample server is the one the README describes.
		`"shares": 4`,
		`"guest": 1`,
		`"pathMissing": 1`,
		`"users": 3`,
		`"disabledUsers": 1`,
		`"sessions": 2`,
		`"openFiles": 2`,
		`"smb1Enabled": false`,
		`"serving": true`,
		`"name": "team"`,
		`"world-writable"`,
		`"name": "carol"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--check output is missing %s", want)
		}
	}

	// And it is JSON a script can walk rather than a shape only a human reads.
	var report map[string]any
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("--check did not print valid JSON: %v", err)
	}
	if _, ok := report["shareList"].([]any); !ok {
		t.Errorf("shareList is not a list: %T", report["shareList"])
	}
}

// TestCheckRunsNothing: --check exists to be safe to run anywhere, including
// in CI against a production-shaped machine, so it must not run a single
// command through the backend.
func TestCheckRunsNothing(t *testing.T) {
	backend := samba.NewFake()
	var out bytes.Buffer
	if err := runCheck(backend, compat.Result{}, &out); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	if ran := backend.Ran(); len(ran) != 0 {
		t.Errorf("--check ran %d commands: %v", len(ran), ran)
	}
	// No mutation may appear in the report either: a command line in it would
	// mean one had been built.
	for _, forbidden := range []string{"smbpasswd", "install -m 644",
		"reload-config"} {
		if strings.Contains(out.String(), forbidden) {
			t.Errorf("--check printed a command it should never build: %s",
				forbidden)
		}
	}
}
