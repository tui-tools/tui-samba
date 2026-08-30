// Command tui-samba is a terminal UI for a Samba file server: the shares it
// exports and what is wrong with them, the accounts that can reach them, and
// who is connected right now. It previews the exact command line of every
// change before running it, and a share it writes is checked by Samba's own
// parser before anybody is asked to confirm anything.
//
// It is a file server tool. Samba as an Active Directory domain controller is
// deliberately out of scope — see the README.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/config"
	"github.com/tui-tools/tui-kit/theme"
	"github.com/tui-tools/tui-samba/internal/samba"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// toolName is the binary name, which is also the configuration directory:
// /etc/tui-samba/config.toml and ~/.config/tui-samba/config.toml.
const toolName = "tui-samba"

// sambaBackend is the manifest's name for the suite whose version gates a read
// path: `smbstatus --json` exists only above a known release.
const sambaBackend = "samba"

// keyConfig overrides where smb.conf is looked for. It is the tool's own
// configuration key, beyond the two the family shares.
const keyConfig = "config"

// version is stamped by the release build (-ldflags "-X main.version=…").
var version = "dev"

// defaults declares the configuration keys tui-samba understands. Only these
// are read from the environment (TUI_SAMBA_SUDO, …), so an unrelated variable
// can never leak into the configuration.
func defaults() map[string]string {
	return map[string]string{
		keyConfig:       "",
		config.KeySudo:  "sudo -n",
		config.KeyTheme: "",
	}
}

// options holds the parsed command line.
type options struct {
	demo        bool
	check       bool
	report      bool
	themePath   string
	sudo        string
	configPath  string
	showVersion bool
	// sudoSet records whether -sudo was passed, so `--sudo ""` can disable
	// escalation instead of reading as "not given".
	sudoSet bool
}

// parseFlags defines and reads the command line.
func parseFlags(args []string, out *os.File) (options, error) {
	var opts options
	fs := flag.NewFlagSet(toolName, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&opts.demo, "demo", false,
		"run against a sample file server, without touching the real one")
	fs.BoolVar(&opts.check, "check", false,
		"read the file server and print the parsed state as JSON, then exit "+
			"(no UI, no changes); exit 1 if the backend cannot be read")
	fs.BoolVar(&opts.report, "report", false, reportUsage)
	fs.StringVar(&opts.themePath, "theme", "",
		"path to an Omarchy-style colors.toml (overrides the config file)")
	fs.StringVar(&opts.sudo, "sudo", "",
		"privilege escalation prefix, e.g. \"sudo -n\" or \"\" to disable")
	fs.StringVar(&opts.configPath, "config", "",
		"path to smb.conf; empty asks the server where its own is")
	fs.BoolVar(&opts.showVersion, "version", false, "print the version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(out, "tui-samba — the Samba file server on this "+
			"machine: shares, accounts and who is connected\n\n"+
			"Usage:\n  tui-samba [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(out, "\nConfiguration is read from %s, then %s, "+
			"then TUI_SAMBA_* in the environment.\n",
			config.SystemPathFor(toolName), config.UserPathFor(toolName))
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "sudo" {
			opts.sudoSet = true
		}
	})
	return opts, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, toolName+":", err)
		os.Exit(1)
	}
}

// run wires the configuration, the backend and the Bubble Tea program.
func run(args []string) error {
	opts, err := parseFlags(args, os.Stdout)
	if err != nil {
		// flag already printed the reason and the usage.
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if opts.showVersion {
		fmt.Println(toolName, version)
		return nil
	}

	cfg, err := config.Load(config.Options{Tool: toolName, Defaults: defaults()})
	if err != nil {
		return err
	}
	applyOverrides(&cfg, opts)

	// The configured theme is handed to the kit through the same variable the
	// user could set by hand, so precedence stays in one place. It is set
	// before the backend is built so --report can name the theme the UI would
	// have used even on a machine where no backend can be.
	if path := cfg.Theme(); path != "" {
		if err := os.Setenv("TUI_THEME", path); err != nil {
			return err
		}
	}

	// --report is the non-interactive path that must work everywhere. It reads
	// nothing privileged and it survives a machine with no Samba at all,
	// because "there is no file server here" is one of the things a bug report
	// has to be able to say. So it comes before the backend is required.
	if opts.report {
		return runReport(cfg, opts, os.Stdout)
	}

	// The server's version is probed once, before the backend is built,
	// because the backend needs the capability set: whether smbstatus can
	// answer in JSON is a version question, and the answer comes from the
	// manifest.
	backendCompat := probeCompat(context.Background(), opts.demo)

	backend, err := pickBackend(cfg, opts, backendCompat.Caps())
	if err != nil {
		return err
	}

	// --check is the non-interactive path: it reads the server and prints, and
	// never starts a terminal program.
	if opts.check {
		return runCheck(backend, backendCompat, os.Stdout)
	}

	program := tea.NewProgram(newApp(backend, theme.New(), backendCompat),
		tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// applyOverrides folds the command line into the configuration, which is the
// last and highest-precedence layer.
func applyOverrides(cfg *config.Config, opts options) {
	if opts.themePath != "" {
		cfg.Set(config.KeyTheme, opts.themePath)
	}
	// An explicitly empty -sudo disables escalation, so the flag is applied
	// whenever it was passed, empty value included.
	if opts.sudoSet {
		cfg.Set(config.KeySudo, opts.sudo)
	}
	if opts.configPath != "" {
		cfg.Set(keyConfig, opts.configPath)
	}
}

// pickBackend returns the demo backend or the real one.
func pickBackend(cfg config.Config, opts options,
	caps compat.Caps) (shares.Backend, error) {
	if opts.demo {
		return samba.NewFake(), nil
	}
	return samba.NewReal(cfg.SudoPrefix(), caps, samba.Options{
		ConfigPath: cfg.String(keyConfig, ""),
	})
}
