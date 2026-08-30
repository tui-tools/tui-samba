package samba

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every function in parse.go turns output tui-samba did not write into the
// model the screen shows and the verdicts it reports: testparm's effective
// configuration, pdbedit's accounts, smbstatus in two shapes, `ss -tlnp`,
// `getsebool`, `stat` and `ls -Zd`. A parser that invents a share name or a
// mode is how a tool ends up reporting a finding about something that is not
// there, or offering to change a stanza it never really read.
//
// `go test` runs the seeds below on every commit; `go test -fuzz=FuzzParseX
// ./internal/samba/` explores past them locally — see
// tui-kit/templates/FUZZING.md for the family rule.

// seedFuzz adds every named testdata file to the corpus, plus the shapes a
// real capture never has: nothing, a lone separator, a truncated line.
func seedFuzz(f *testing.F, names ...string) {
	f.Helper()
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // the name is a literal in the tests, and testdata is in the repository
		if err != nil {
			f.Fatalf("read fixture %s: %v", name, err)
		}
		f.Add(string(raw))
	}
	f.Add("")
	f.Add("\n\n\n")
	f.Add("=")
	f.Add(":")
	f.Add("[]")
	f.Add("{}")
}

// checkKeys asserts what every caller of a parameter map is allowed to assume:
// a key it can look up by the name Samba documents — lower case, single
// spaces, never blank.
func checkKeys(t *testing.T, params map[string]string, where string) {
	t.Helper()
	for key := range params {
		if key == "" {
			t.Fatalf("%s carries a blank parameter name", where)
		}
		if key != normalizeKey(key) {
			t.Fatalf("%s key %q is not normalised", where, key)
		}
	}
}

// FuzzParseTestparm covers the read the whole tool is built on: the shares it
// lists are the shares the screen offers to edit.
func FuzzParseTestparm(f *testing.F) {
	seedFuzz(f, "testparm-s.txt")
	f.Fuzz(func(t *testing.T, out string) {
		global, list := ParseTestparm(out)
		if global.Params == nil {
			t.Fatal("global parameters are nil, which a caller cannot range over")
		}
		checkKeys(t, global.Params, "global")
		seen := map[string]bool{}
		for _, share := range list {
			if share.Name == "" {
				t.Fatalf("share with no name: %+v", share)
			}
			if strings.EqualFold(share.Name, "global") {
				t.Fatal("the global section was returned as a share")
			}
			if share.Params == nil {
				t.Fatalf("share %q has nil parameters", share.Name)
			}
			checkKeys(t, share.Params, "share "+share.Name)
			// Raw is what the editor writes back, so it has to open on the
			// stanza the share is named after.
			if len(share.Raw) == 0 || share.Raw[0] != "["+share.Name+"]" {
				t.Fatalf("share %q raw text does not open on its stanza: %q",
					share.Name, share.Raw)
			}
			if seen[share.Name] {
				continue
			}
			seen[share.Name] = true
		}
	})
}

func FuzzParseIncludes(f *testing.F) {
	seedFuzz(f, "testparm-s.txt")
	f.Add("[global]\n\tinclude = /etc/samba/extra.conf\n")
	f.Add("include=\n; include = /commented\n")
	f.Fuzz(func(t *testing.T, raw string) {
		for _, include := range ParseIncludes(raw) {
			if include == "" {
				t.Fatal("returned an empty include path")
			}
			if include != strings.TrimSpace(include) {
				t.Fatalf("include path is not trimmed: %q", include)
			}
			if strings.Contains(include, "\n") {
				t.Fatalf("include path spans lines: %q", include)
			}
		}
	})
}

func FuzzParseServerVersion(f *testing.F) {
	seedFuzz(f, "smbd-version.txt")
	f.Add("Version 4.20.5\n")
	f.Fuzz(func(t *testing.T, out string) {
		version := ParseServerVersion(out)
		if strings.Contains(version, "\n") {
			t.Fatalf("version spans lines: %q", version)
		}
		if version != strings.TrimSpace(version) {
			t.Fatalf("version is not trimmed: %q", version)
		}
	})
}

// FuzzParsePdbedit matters because a disabled account and a working one look
// identical apart from the flags this parser reads.
func FuzzParsePdbedit(f *testing.F) {
	seedFuzz(f, "pdbedit-lv.txt")
	f.Add("---------------\nUnix username:        sam\nAccount Flags:        [DN         ]\n")
	f.Fuzz(func(t *testing.T, out string) {
		for _, user := range ParsePdbedit(out) {
			if user.Name == "" {
				t.Fatalf("account with no name: %+v", user)
			}
			if strings.ContainsAny(user.Flags, "[]\n") {
				t.Fatalf("account flags kept their brackets: %q", user.Flags)
			}
			if user.Disabled != strings.Contains(user.Flags, "D") {
				t.Fatalf("disabled disagrees with flags %q", user.Flags)
			}
			if user.NoPassword != strings.Contains(user.Flags, "N") {
				t.Fatalf("no-password disagrees with flags %q", user.Flags)
			}
		}
	})
}

func FuzzParseStatusJSON(f *testing.F) {
	seedFuzz(f, "smbstatus.json")
	f.Fuzz(func(t *testing.T, out string) {
		sessions, connections, files, err := ParseStatusJSON(out)
		if err != nil {
			if sessions != nil || connections != nil || files != nil {
				t.Fatal("failed and still returned rows")
			}
			return
		}
		// The crypto columns are the reason the JSON path exists: an empty
		// cell there would read as a value nobody managed to read.
		for _, s := range sessions {
			if s.Encryption == "" || s.Signing == "" {
				t.Fatalf("session with a blank crypto column: %+v", s)
			}
		}
		for _, c := range connections {
			if c.Encryption == "" || c.Signing == "" {
				t.Fatalf("connection with a blank crypto column: %+v", c)
			}
		}
	})
}

func FuzzParseStatusText(f *testing.F) {
	seedFuzz(f, "smbstatus-text.txt")
	f.Fuzz(func(t *testing.T, out string) {
		sessions, connections, files := ParseStatusText(out)
		// This parser skips a row it cannot read with confidence, so every
		// row it does return carries the process it belongs to.
		for _, s := range sessions {
			if !isDigits(s.PID) {
				t.Fatalf("session PID is not a number: %q", s.PID)
			}
		}
		for _, c := range connections {
			if !isDigits(c.PID) {
				t.Fatalf("connection PID is not a number: %q", c.PID)
			}
			if c.Service == "" {
				t.Fatalf("connection with no service: %+v", c)
			}
		}
		for _, file := range files {
			if !isDigits(file.PID) {
				t.Fatalf("open file PID is not a number: %q", file.PID)
			}
			if !strings.HasPrefix(file.Path, "/") {
				t.Fatalf("open file path is not absolute: %q", file.Path)
			}
		}
	})
}

// isDigits reports whether the value is a decimal number, which is what every
// PID column of smbstatus holds.
func isDigits(value string) bool {
	if value == "" {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func FuzzParseProperties(f *testing.F) {
	seedFuzz(f, "systemctl-show-smb.txt")
	f.Fuzz(func(t *testing.T, out string) {
		for key := range ParseProperties(out) {
			if key == "" {
				t.Fatal("returned a blank property name")
			}
			if strings.Contains(key, "\n") {
				t.Fatalf("property name spans lines: %q", key)
			}
		}
	})
}

// FuzzParseListening covers the read that answers "is anything actually
// listening": a port this returns is a port the screen calls open.
func FuzzParseListening(f *testing.F) {
	seedFuzz(f, "ss-tlnp.txt")
	f.Add("LISTEN 0 50 0.0.0.0:445 0.0.0.0:* users:((\"smbd\",pid=1,fd=2))\n")
	f.Fuzz(func(t *testing.T, out string) {
		seen := map[string]bool{}
		for _, port := range ParseListening(out) {
			if port.Port != 139 && port.Port != 445 {
				t.Fatalf("kept a port a file server is not reached on: %d", port.Port)
			}
			if port.Address == "" {
				t.Fatalf("port with no local address: %+v", port)
			}
			key := port.Address + ":" + strconv.Itoa(port.Port)
			if seen[key] {
				t.Fatalf("the same socket was reported twice: %s", key)
			}
			seen[key] = true
			if strings.ContainsAny(port.Process, "\n\"") {
				t.Fatalf("process name is not a program name: %q", port.Process)
			}
		}
	})
}

func FuzzParseBooleans(f *testing.F) {
	seedFuzz(f, "getsebool.txt")
	f.Add("samba_enable_home_dirs --> on\n")
	f.Fuzz(func(t *testing.T, out string) {
		for name := range ParseBooleans(out) {
			if name == "" {
				t.Fatal("returned a boolean with no name")
			}
			if name != strings.TrimSpace(name) {
				t.Fatalf("boolean name is not trimmed: %q", name)
			}
		}
	})
}

// FuzzParseStat is the one whose answer becomes a finding: the mode it reads
// is what the screen calls world-writable or not.
func FuzzParseStat(f *testing.F) {
	seedFuzz(f, "stat-share.txt")
	f.Add("755 root root directory\n")
	f.Add("0777 nobody nogroup directory\n")
	f.Fuzz(func(t *testing.T, out string) {
		info, err := ParseStat(out)
		if err != nil {
			if info.Exists || info.Mode != "" || info.Owner != "" || info.Group != "" {
				t.Fatalf("failed and still described a directory: %+v", info)
			}
			return
		}
		if !info.Exists {
			t.Fatal("succeeded and reported that nothing is there")
		}
		if len(info.Mode) < 4 {
			t.Fatalf("mode is not padded to four digits: %q", info.Mode)
		}
		mode, convErr := strconv.ParseUint(info.Mode, 8, 32)
		if convErr != nil {
			t.Fatalf("mode is not octal: %q", info.Mode)
		}
		if info.WorldWritable != (mode&0o002 != 0) {
			t.Fatalf("world-writable disagrees with mode %q", info.Mode)
		}
		if info.Owner == "" || info.Group == "" {
			t.Fatalf("succeeded with no owner or group: %+v", info)
		}
	})
}

// labelRe is the shape of a full SELinux label, anchored, which contextRe is
// not: whatever ParseContext returns is the label the tool then reasons about.
var labelRe = regexp.MustCompile(
	`^[a-z_]+_u:[a-z_]+_r:[a-z_]+_t:[A-Za-z0-9:.,_-]+$`)

func FuzzParseContext(f *testing.F) {
	seedFuzz(f, "ls-z.txt")
	f.Add("unconfined_u:object_r:samba_share_t:s0 /srv/share\n")
	f.Fuzz(func(t *testing.T, out string) {
		context := ParseContext(out)
		if context == "" {
			return
		}
		if !labelRe.MatchString(context) {
			t.Fatalf("returned something that is not a full label: %q", context)
		}
	})
}
