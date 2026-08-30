package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/manifest"
	"github.com/tui-tools/tui-kit/runner"
	tuisamba "github.com/tui-tools/tui-samba"
	"github.com/tui-tools/tui-samba/internal/samba"
)

// loadManifest reads the manifest the binary really carries.
func loadManifest(t *testing.T) manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(tuisamba.ManifestJSON)
	if err != nil {
		t.Fatalf("the embedded manifest does not parse: %v", err)
	}
	if m.Name != toolName {
		t.Fatalf("manifest name = %q, want %q", m.Name, toolName)
	}
	return m
}

// sambaBlock loads the one backend block by name.
func sambaBlock(t *testing.T) compat.Backend {
	t.Helper()
	b, ok := loadManifest(t).Backend(sambaBackend)
	if !ok {
		t.Fatalf("the manifest declares no %q backend", sambaBackend)
	}
	return b
}

// TestManifestDeclaresTheServer: the block is what the probe, the header and
// the README's compatibility table all read, so a mismatch between it and the
// binary the backend drives would be one nobody could ever be told about.
func TestManifestDeclaresTheServer(t *testing.T) {
	b := sambaBlock(t)
	if b.Binary != samba.BinSmbd {
		t.Errorf("binary = %q, want %q", b.Binary, samba.BinSmbd)
	}
	if len(b.VersionCommand) == 0 {
		t.Errorf("a backend with no version command cannot be probed")
	}
	if b.Minimum == "" {
		t.Errorf("no minimum version is declared")
	}
	// smbd lives in an sbin directory on every distribution, so an ordinary
	// user's PATH does not carry it. Without the fallbacks the probe would
	// report "not installed" on a machine that is running a file server.
	var sbin bool
	for _, path := range b.SearchPaths {
		if strings.Contains(path, "sbin/") {
			sbin = true
		}
	}
	if !sbin {
		t.Errorf("no sbin fallback is declared for smbd: %v", b.SearchPaths)
	}
}

// TestVersionRegexReadsRealOutput uses the banner smbd really prints. The
// distribution suffix is what catches a lazy regex: a version of
// "4.19.5-Ubuntu" has to come back as 4.19.5.
func TestVersionRegexReadsRealOutput(t *testing.T) {
	b := sambaBlock(t)
	for _, test := range []struct{ output, want string }{
		{"Version 4.20.5", "4.20.5"},
		{"Version 4.19.5-Ubuntu", "4.19.5"},
		{"Version 4.11.6", "4.11.6"},
		{"Version 4.17.0pre1-GIT-a0f12b9c80b", "4.17.0"},
	} {
		if got := compat.ParseVersion(test.output, b.VersionRegex); got != test.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", test.output, got, test.want)
		}
	}
}

// TestStatusJSONGateMatchesTheRelease pins what the manifest claims:
// `smbstatus --json` arrived in Samba 4.17, and every older server is read
// through the text output instead.
func TestStatusJSONGateMatchesTheRelease(t *testing.T) {
	b := sambaBlock(t)
	for version, want := range map[string]bool{
		"4.11.6":  false,
		"4.15.13": false,
		"4.16.11": false,
		"4.17.0":  true,
		"4.20.5":  true,
	} {
		caps := compat.NewCaps(version, b.Features)
		if got := caps.Has(samba.FeatureStatusJSON); got != want {
			t.Errorf("Samba %s: status-json = %v, want %v", version, got, want)
		}
	}
	if since, ok := compat.NewCaps("4.20.5", b.Features).
		Since(samba.FeatureStatusJSON); !ok || since != "4.17" {
		t.Errorf("the feature is declared since %q", since)
	}
}

// TestUnknownVersionKeepsEveryFeature: a version the probe could not read must
// not hide a working read path. The backend refuses in its own words instead,
// and falls back to the text output when the JSON one answers with an error.
func TestUnknownVersionKeepsEveryFeature(t *testing.T) {
	caps := compat.Result{}.Caps()
	if !caps.Has(samba.FeatureStatusJSON) {
		t.Errorf("an unprobed Samba must be treated as capable")
	}
}

func TestProbeInDemoModeReportsNothing(t *testing.T) {
	if got := probeCompat(context.Background(), true); got.Backend != "" {
		t.Errorf("--demo probed the host: %+v", got)
	}
}

// TestProbeReportsTheServerThisMachineHas: most machines have no Samba, and
// what the probe must never do is report on one that is not there.
func TestProbeReportsTheServerThisMachineHas(t *testing.T) {
	result := probeCompat(context.Background(), false)
	b := sambaBlock(t)
	if !runner.Available(b.Binary, b.SearchPaths...) {
		if result.Version != "" {
			t.Errorf("the probe read a version on a machine with no smbd: %+v",
				result)
		}
		return
	}
	if result.Version != "" && !versionShape.MatchString(result.Version) {
		t.Errorf("the probe read %q, which is not a version", result.Version)
	}
}

// versionShape is what a version looks like once the manifest's regex has had
// it.
var versionShape = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}$`)

// TestNotesCoverTheRanges: every caveat the README prints has to apply to some
// version anybody runs, or it is documentation nobody will ever be shown.
func TestNotesCoverTheRanges(t *testing.T) {
	b := sambaBlock(t)
	if len(b.Notes) == 0 {
		t.Fatalf("the samba backend declares no notes")
	}
	candidates := []string{"4.11.6", "4.15.13", "4.16.11", "4.17.0", "4.20.5"}
	for _, note := range b.Notes {
		if strings.TrimSpace(note.Impact) == "" {
			t.Errorf("note %q has no impact sentence", note.Range)
		}
		var matched bool
		for _, version := range candidates {
			if compat.Match(version, note.Range) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("note %q applies to no version anyone runs", note.Range)
		}
	}
}

// TestTheManifestDoesNotClaimADomainController: the scope of this tool is a
// file server, and the description is where a reader is told so before they
// install it expecting samba-tool.
func TestTheManifestDoesNotClaimADomainController(t *testing.T) {
	m := loadManifest(t)
	if m.Category != "file-sharing" {
		t.Errorf("category = %q", m.Category)
	}
	if !strings.Contains(strings.ToLower(tuisambaDescription(t)),
		"active directory") {
		t.Errorf("the description does not say what is out of scope")
	}
}

// tuisambaDescription reads the manifest's description straight out of the
// embedded bytes, because the runtime manifest type does not carry it.
func tuisambaDescription(t *testing.T) string {
	t.Helper()
	const key = `"description": `
	body := string(tuisamba.ManifestJSON)
	start := strings.Index(body, key)
	if start < 0 {
		t.Fatalf("the manifest carries no description")
	}
	rest := body[start+len(key):]
	end := strings.Index(rest[1:], `",`)
	if end < 0 {
		t.Fatalf("the description is not a JSON string")
	}
	return rest[:end+2]
}
