package main

import (
	"context"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/manifest"
	tuisamba "github.com/tui-tools/tui-samba"
)

// probeCompat reads the version of the Samba suite this tool drives.
//
// There is one backend block rather than one per program, because there is one
// version: testparm, pdbedit, smbpasswd, smbstatus and smbcontrol ship
// together and are released together, and a machine with two of them at
// different versions is a machine somebody broke by hand.
//
// The version comes from `smbd --version`, which prints "Version 4.20.5" and
// nothing else. smbd is the interesting part of that: it lives in /usr/sbin on
// every distribution, so an ordinary user's PATH does not carry it — hence the
// search paths in the manifest, which the kit's probe honours.
//
// What the version is judged against — the minimum, the versions the lab has
// actually run against, the caveats that apply to a range — comes from the
// repository's own tool.json, embedded in the binary, so there is no second
// copy of them in the code.
//
// It never fails. A manifest that cannot be parsed produces an empty Result,
// and a machine with no Samba produces one with no version and the reason: on
// a tool about a file server, "there is no file server here" is an answer
// worth showing rather than an error.
func probeCompat(ctx context.Context, demo bool) compat.Result {
	// --demo drives an in-memory file server; probing the real Samba on the
	// host would report a version that has nothing to do with what is on
	// screen.
	if demo {
		return compat.Result{}
	}
	m, err := manifest.Load(tuisamba.ManifestJSON)
	if err != nil {
		return compat.Result{}
	}
	backend, ok := m.Backend(sambaBackend)
	if !ok {
		return compat.Result{}
	}
	return compat.Probe(ctx, backend)
}
