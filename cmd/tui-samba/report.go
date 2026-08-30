package main

import (
	"context"
	"fmt"
	"io"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/config"
	"github.com/tui-tools/tui-kit/report"
	"github.com/tui-tools/tui-kit/theme"
)

// statusJSONFeature is the manifest feature that decides how the connections
// are read. It is the one capability a version gates in this tool, so it is
// the one worth a line in a bug report.
const statusJSONFeature = "status-json"

// runReport prints the block a bug report needs and exits. Everything generic
// — the kit version, the distribution, the kernel, the terminal, where the
// binary came from — is collected by the kit, so the whole family answers
// --report in the same shape. What this function adds is the part only
// tui-samba knows: the version the compat probe read off smbd, which read path
// that version gets, and whether smb.conf was looked for where the server keeps
// it or somewhere the configuration named.
//
// It never reads the file server. --check is the flag that does that, and most
// of it needs privileges; a report has to work for a user who cannot get them,
// because the missing privilege may be the bug. For the same reason a machine
// with no Samba at all still gets a report: "there is no file server here" is
// most machines, and it is a fact a report should carry rather than refuse over.
func runReport(cfg config.Config, opts options, out io.Writer) error {
	palette, _ := theme.ResolvePalette()

	// The same probe --check and the header use. There is one version probe in
	// this tool and this is it.
	backendCompat := probeCompat(context.Background(), opts.demo)

	var backendName, selectError string
	if backend, err := pickBackend(cfg, opts, backendCompat.Caps()); err != nil {
		selectError = err.Error()
	} else {
		backendName = backend.Name()
	}

	info := report.Info{
		Tool:           toolName,
		Version:        version,
		Backend:        backendName,
		BackendVersion: backendCompat.Version,
		BackendDetail:  backendCompat.Detail,
		Demo:           opts.demo,
		Sudo:           cfg.String(config.KeySudo, ""),
		Theme:          palette.Name,
	}
	if opts.demo {
		// The fake imitates the real suite, and it answers Name() with "samba"
		// like the real backend does. Saying "demo" on the backend line and
		// naming the imitated suite beside it is what keeps a demo report from
		// reading as a report about this machine's Samba.
		info.Backend = "demo"
		info.Extra = append(info.Extra, report.Field{
			Key: "demo backend", Value: sambaBackend,
		})
	} else {
		info.Extra = append(info.Extra, report.Field{
			Key: "connections", Value: connectionsLine(backendCompat.Caps()),
		})
	}
	info.Extra = append(info.Extra, report.Field{
		Key: "smb.conf", Value: configLine(cfg),
	})
	if selectError != "" {
		info.Extra = append(info.Extra, report.Field{
			Key: "backend error", Value: selectError,
		})
	}

	_, err := io.WriteString(out, report.Render(info))
	return err
}

// connectionsLine says which of the two connection readers the probed version
// gets. A session that shows the wrong dialect on 4.16 and the right one on
// 4.17 is not two bugs, and this line is what tells them apart in one look.
func connectionsLine(caps compat.Caps) string {
	if caps.Has(statusJSONFeature) {
		return "smbstatus --json"
	}
	since, _ := caps.Since(statusJSONFeature)
	return "smbstatus text output (--json needs " + since + ")"
}

// configLine says where smb.conf was looked for. The path itself is not
// printed when the configuration names one: it is a value the user chose and
// may sit under a home directory, which this block does not publish. Whether
// the server was asked for its own is the part that changes what gets parsed.
func configLine(cfg config.Config) string {
	if cfg.String(keyConfig, "") != "" {
		return "set by configuration"
	}
	return "as the server reports it"
}

// reportUsage is the flag's one-line help, kept here next to what it prints.
var reportUsage = fmt.Sprintf(
	"print the versions and machine facts a bug report needs, then exit "+
		"(no UI, no privileges, nothing about you: paste it into a %s issue)",
	toolName)
