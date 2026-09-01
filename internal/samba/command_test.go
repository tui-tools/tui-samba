package samba

import (
	"strings"
	"testing"

	"github.com/tui-tools/tui-samba/internal/shares"
)

// TestArgvIsExactlyWhatIsPreviewed is the family's central promise at the
// level it is built: every command this package produces is a fixed argv, so
// the string the confirm dialog shows is the string that is executed.
func TestArgvIsExactlyWhatIsPreviewed(t *testing.T) {
	tests := []struct {
		name string
		make func() (shares.Command, error)
		want string
	}{
		{
			name: "check a staged file",
			make: func() (shares.Command, error) {
				return BuildValidate("/tmp/tui-samba-1/team.conf")
			},
			want: "testparm -s /tmp/tui-samba-1/team.conf",
		},
		{
			name: "install a drop-in",
			make: func() (shares.Command, error) {
				return BuildInstall("/tmp/tui-samba-1/team.conf",
					"/etc/samba/tui-samba.d/team.conf")
			},
			want: "install -m 644 /tmp/tui-samba-1/team.conf " +
				"/etc/samba/tui-samba.d/team.conf",
		},
		{
			name: "install over the server's own configuration",
			make: func() (shares.Command, error) {
				return BuildInstall("/tmp/tui-samba-1/smb.conf", "/etc/samba/smb.conf")
			},
			want: "install -m 644 /tmp/tui-samba-1/smb.conf /etc/samba/smb.conf",
		},
		{
			name: "create the drop-in directory",
			make: func() (shares.Command, error) {
				return BuildMakeDropInDir(), nil
			},
			want: "install -d -m 755 /etc/samba/tui-samba.d",
		},
		{
			name: "remove a drop-in this tool wrote",
			make: func() (shares.Command, error) {
				return BuildRemoveDropIn("/etc/samba/tui-samba.d/team.conf")
			},
			want: "rm -f -- /etc/samba/tui-samba.d/team.conf",
		},
		{
			name: "create the directory a new share exports",
			make: func() (shares.Command, error) {
				return BuildMakeShareDir("/srv/photos", "root", "staff")
			},
			want: "install -d -m 2775 -o root -g staff /srv/photos",
		},
		{
			name: "label it for SELinux",
			make: func() (shares.Command, error) {
				return BuildLabelShareDir("/srv/photos")
			},
			want: "chcon -t samba_share_t /srv/photos",
		},
		{
			name: "reload",
			make: func() (shares.Command, error) { return BuildReload(), nil },
			want: "smbcontrol all reload-config",
		},
		{
			name: "self-test",
			make: func() (shares.Command, error) { return BuildSelfTest("localhost") },
			want: "smbclient -L localhost -N",
		},
		{
			name: "add an account",
			make: func() (shares.Command, error) {
				return BuildUserAdd("alice", "correct horse battery staple")
			},
			want: "smbpasswd -a -s alice",
		},
		{
			name: "set a password",
			make: func() (shares.Command, error) {
				return BuildSetPassword("alice", "correct horse battery staple")
			},
			want: "smbpasswd -s alice",
		},
		{
			name: "remove an account",
			make: func() (shares.Command, error) {
				return BuildUserAction("carol", shares.UserDelete)
			},
			want: "smbpasswd -x carol",
		},
		{
			name: "disable an account",
			make: func() (shares.Command, error) {
				return BuildUserAction("carol", shares.UserDisable)
			},
			want: "smbpasswd -d carol",
		},
		{
			name: "enable an account",
			make: func() (shares.Command, error) {
				return BuildUserAction("carol", shares.UserEnable)
			},
			want: "smbpasswd -e carol",
		},
	}
	for _, test := range tests {
		cmd, err := test.make()
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if got := cmd.String(); got != test.want {
			t.Errorf("%s: argv = %q, want %q", test.name, got, test.want)
		}
		if strings.TrimSpace(cmd.Description) == "" {
			t.Errorf("%s: no description for the dialog", test.name)
		}
	}
}

// TestThePasswordIsNeverInTheCommandLine is the reason smbpasswd is driven
// with `-s`: a command line is visible in `ps` to every account on the
// machine, and a password on one is a password disclosed.
func TestThePasswordIsNeverInTheCommandLine(t *testing.T) {
	const password = "hunter2-and-then-some"
	for _, cmd := range mustCommands(t,
		func() (shares.Command, error) { return BuildUserAdd("alice", password) },
		func() (shares.Command, error) { return BuildSetPassword("alice", password) },
	) {
		if strings.Contains(cmd.String(), password) {
			t.Errorf("the password reached the command line: %s", cmd.String())
		}
		for _, arg := range cmd.Argv {
			if arg == password {
				t.Errorf("the password is an argv element: %v", cmd.Argv)
			}
		}
		if strings.Contains(cmd.Description, password) {
			t.Errorf("the password reached the dialog's description")
		}
		// It has to be somewhere, and standard input is where: twice, because
		// smbpasswd asks for it and then asks again.
		if cmd.Stdin != password+"\n"+password+"\n" {
			t.Errorf("stdin = %q, want the password and its confirmation",
				cmd.Stdin)
		}
	}
}

// mustCommands builds several commands or fails the test.
func mustCommands(t *testing.T,
	makers ...func() (shares.Command, error)) []shares.Command {
	t.Helper()
	var out []shares.Command
	for _, make := range makers {
		cmd, err := make()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		out = append(out, cmd)
	}
	return out
}

func TestCheckPassword(t *testing.T) {
	if err := CheckPassword(""); err == nil {
		t.Errorf("an empty password was accepted")
	}
	// A newline would be read as the end of the password and the start of the
	// answer to the retype prompt.
	if err := CheckPassword("first\nsecond"); err == nil {
		t.Errorf("a password with a newline was accepted")
	}
	if err := CheckPassword(strings.Repeat("x", 256)); err == nil {
		t.Errorf("a 256 character password was accepted")
	}
	if err := CheckPassword("a space and a $ and a #"); err != nil {
		t.Errorf("an ordinary password was refused: %v", err)
	}
}

func TestCheckShareName(t *testing.T) {
	for _, name := range []string{"team", "team-2", "Team_A", "archive.old"} {
		if err := CheckShareName(name); err != nil {
			t.Errorf("CheckShareName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{
		"", "global", "homes", "printers", "print$",
		"a b", "../escape", "team]\n[global", "-dash", "team/sub",
		// The file this tool keeps for the server-wide settings: a share of
		// that name would be written over them.
		"tui-samba-global", "TUI-Samba-Global",
	} {
		if err := CheckShareName(name); err == nil {
			t.Errorf("CheckShareName(%q) was accepted", name)
		}
	}
}

func TestCheckPath(t *testing.T) {
	for _, path := range []string{"/srv/team", "/srv/Team Files", "/srv/a-b_c.d"} {
		if err := CheckPath(path); err != nil {
			t.Errorf("CheckPath(%q) = %v", path, err)
		}
	}
	for _, path := range []string{
		"", "srv/team", "/srv/../etc", "/srv/team\n[global]", "/srv/team#comment",
	} {
		if err := CheckPath(path); err == nil {
			t.Errorf("CheckPath(%q) was accepted", path)
		}
	}
}

func TestCheckUser(t *testing.T) {
	for _, name := range []string{"alice", "svc-backup", "@staff", "+staff",
		"host$", "_apt"} {
		if err := CheckUser(name); err != nil {
			t.Errorf("CheckUser(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "Alice", "a b", "alice;rm", "-x"} {
		if err := CheckUser(name); err == nil {
			t.Errorf("CheckUser(%q) was accepted", name)
		}
	}
}

// TestInstallRefusesADestinationOutsideTheTwoItOwns: the only two files this
// tool writes are the server's own configuration and a drop-in of its own, and
// nothing that reaches an argv may widen that.
func TestInstallRefusesADestinationOutsideTheTwoItOwns(t *testing.T) {
	for _, destination := range []string{
		"/etc/passwd",
		"/etc/samba/smb.conf.bak",
		"/etc/samba/tui-samba.d/../../shadow",
		"/etc/samba/tui-samba.d/team.txt",
	} {
		if _, err := BuildInstall("/tmp/tui-samba-1/x.conf", destination); err == nil {
			t.Errorf("installing over %q was allowed", destination)
		}
	}
	// And the source has to be a staging path this package built.
	if _, err := BuildInstall("relative/path", ConfigPath); err == nil {
		t.Errorf("a relative source was allowed")
	}
}

func TestRenderShare(t *testing.T) {
	stanza, err := RenderShare(shares.ShareRequest{
		Name: "team", Path: "/srv/team", Comment: "Shared working files",
		Browseable: "yes", ReadOnly: "no", GuestOK: "no",
		ValidUsers: "alice, bob", WriteList: "alice",
		CreateMask: "664", DirectoryMask: "2775",
	}, []string{"\tvfs objects = recycle"})
	if err != nil {
		t.Fatalf("RenderShare: %v", err)
	}
	want := []string{
		"[team]",
		"\tcomment = Shared working files",
		"\tpath = /srv/team",
		"\tbrowseable = yes",
		"\tread only = no",
		"\tguest ok = no",
		"\tvalid users = alice bob",
		"\twrite list = alice",
		"\tcreate mask = 0664",
		"\tdirectory mask = 2775",
		// Whatever the stanza already carried and this form has no field for.
		"\tvfs objects = recycle",
	}
	if strings.Join(stanza, "\n") != strings.Join(want, "\n") {
		t.Errorf("stanza =\n%s\nwant\n%s", strings.Join(stanza, "\n"),
			strings.Join(want, "\n"))
	}
}

func TestRenderShareRefusesWhatWouldSmuggleADirective(t *testing.T) {
	base := shares.ShareRequest{
		Name: "team", Path: "/srv/team",
		Browseable: "yes", ReadOnly: "yes", GuestOK: "no",
	}
	comment := base
	comment.Comment = "hello\n[global]\n\tsecurity = share"
	if _, err := RenderShare(comment, nil); err == nil {
		t.Errorf("a comment carrying a second section was accepted")
	}

	users := base
	users.ValidUsers = "alice; rm -rf /"
	if _, err := RenderShare(users, nil); err == nil {
		t.Errorf("an access list entry that is not an account was accepted")
	}

	mask := base
	mask.CreateMask = "rwx"
	if _, err := RenderShare(mask, nil); err == nil {
		t.Errorf("a mask that is not octal was accepted")
	}

	boolean := base
	boolean.ReadOnly = "maybe"
	if _, err := RenderShare(boolean, nil); err == nil {
		t.Errorf("a boolean that is neither yes nor no was accepted")
	}
}

// TestKeepLinesKeepsWhatTheFormHasNoFieldFor is what makes an edit an edit
// rather than a rewrite: a share with a parameter this form never asks about
// keeps it.
func TestKeepLinesKeepsWhatTheFormHasNoFieldFor(t *testing.T) {
	stanza := []string{
		"[team]",
		"\t# somebody's note",
		"\tcomment = Team share",
		"\tpath = /srv/team",
		"\tread only = No",
		"\tvfs objects = recycle",
		"\thosts allow = 192.168.1.",
		"\tCreate Mask = 0664",
	}
	kept := strings.Join(KeepLines(stanza), "\n")
	for _, want := range []string{"# somebody's note", "vfs objects = recycle",
		"hosts allow = 192.168.1."} {
		if !strings.Contains(kept, want) {
			t.Errorf("KeepLines dropped %q:\n%s", want, kept)
		}
	}
	// The managed ones are dropped whatever their spelling: Samba reads a
	// parameter name case-insensitively and ignores its internal spaces.
	for _, unwanted := range []string{"comment =", "path =", "read only =",
		"Create Mask ="} {
		if strings.Contains(kept, unwanted) {
			t.Errorf("KeepLines kept a managed parameter %q:\n%s", unwanted, kept)
		}
	}
}

func TestReplaceStanza(t *testing.T) {
	existing := "# the distribution's own file\n\n[global]\n\tworkgroup = WG\n\n" +
		"[team]\n\tpath = /srv/old\n\tread only = Yes\n\n" +
		"[public]\n\tpath = /srv/public\n"

	updated, err := ReplaceStanza(existing, "team",
		[]string{"[team]", "\tpath = /srv/team", "\tread only = no"})
	if err != nil {
		t.Fatalf("ReplaceStanza: %v", err)
	}
	if strings.Contains(updated, "/srv/old") {
		t.Errorf("the old stanza survived:\n%s", updated)
	}
	// Everything else is untouched, which is the whole point of rewriting one
	// section rather than regenerating the file.
	for _, want := range []string{"# the distribution's own file",
		"[global]", "workgroup = WG", "[public]", "/srv/public"} {
		if !strings.Contains(updated, want) {
			t.Errorf("ReplaceStanza lost %q:\n%s", want, updated)
		}
	}

	// A share the file does not have is appended.
	added, err := ReplaceStanza(existing, "archive",
		[]string{"[archive]", "\tpath = /srv/archive"})
	if err != nil {
		t.Fatalf("ReplaceStanza: %v", err)
	}
	if !strings.Contains(added, "[archive]") {
		t.Errorf("a new share was not appended:\n%s", added)
	}
	if !strings.HasSuffix(added, "\n") {
		t.Errorf("the file does not end in a newline")
	}
}

func TestAddInclude(t *testing.T) {
	existing := "[global]\n\tworkgroup = WG\n\n[public]\n\tpath = /srv/public\n"
	line := IncludeLineFor("team")

	updated, err := AddInclude(existing, line)
	if err != nil {
		t.Fatalf("AddInclude: %v", err)
	}
	// It has to land inside [global] and before the first share, because an
	// include is processed where it appears.
	globalAt := strings.Index(updated, "[global]")
	includeAt := strings.Index(updated, line)
	publicAt := strings.Index(updated, "[public]")
	if includeAt < globalAt || includeAt > publicAt {
		t.Errorf("the include line did not land inside [global]:\n%s", updated)
	}

	// Adding it twice adds it once.
	again, err := AddInclude(updated, line)
	if err != nil {
		t.Fatalf("AddInclude: %v", err)
	}
	if again != updated {
		t.Errorf("the include line was added a second time:\n%s", again)
	}
	if strings.Count(again, line) != 1 {
		t.Errorf("the include line appears %d times", strings.Count(again, line))
	}
}

func TestAddIncludeRefusesASecondDirective(t *testing.T) {
	if _, err := AddInclude("[global]\n", "include = /a\n\tsecurity = share"); err == nil {
		t.Errorf("an include line carrying a newline was accepted")
	}
}

func TestDropInPaths(t *testing.T) {
	if got := DropInFor("team"); got != "/etc/samba/tui-samba.d/team.conf" {
		t.Errorf("DropInFor = %q", got)
	}
	if got := IncludeLineFor("team"); got !=
		"include = /etc/samba/tui-samba.d/team.conf" {
		t.Errorf("IncludeLineFor = %q", got)
	}
}

func TestRenderDropInCarriesItsBanner(t *testing.T) {
	body := RenderDropIn([]string{"[team]", "\tpath = /srv/team"})
	if !strings.HasPrefix(body, "# Written by tui-samba.") {
		t.Errorf("a file this tool created carries no banner:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Errorf("the file does not end in a newline")
	}
}

// TestRemoveDropInRefusesEverythingButItsOwnFiles: the removal path reaches an
// argv with a path in it, and the only path it may ever carry is a .conf this
// tool wrote. The server's own configuration is in the list on purpose —
// smb.conf is rewritten, never deleted.
func TestRemoveDropInRefusesEverythingButItsOwnFiles(t *testing.T) {
	for _, path := range []string{
		"/etc/samba/smb.conf",
		"/etc/passwd",
		"/etc/samba/tui-samba.d/../../shadow",
		"/etc/samba/tui-samba.d/team.txt",
		"/etc/samba/tui-samba.d/",
		"team.conf",
	} {
		if _, err := BuildRemoveDropIn(path); err == nil {
			t.Errorf("removing %q was allowed", path)
		}
	}
	if _, err := BuildRemoveDropIn(DropInFor("team")); err != nil {
		t.Errorf("removing this tool's own drop-in was refused: %v", err)
	}
}

func TestMakeShareDirRefusesWhatWouldReachTheCommandLine(t *testing.T) {
	if _, err := BuildMakeShareDir("/srv/x; rm -rf /", "root", "root"); err == nil {
		t.Errorf("a path carrying a second command was accepted")
	}
	if _, err := BuildMakeShareDir("/srv/photos", "root; id", "root"); err == nil {
		t.Errorf("an owner that is not an account name was accepted")
	}
	if _, err := BuildMakeShareDir("/srv/photos", "root", "-rf"); err == nil {
		t.Errorf("a group that is not an account name was accepted")
	}
}

func TestCheckWorkgroup(t *testing.T) {
	for _, value := range []string{"WORKGROUP", "office", "OFFICE-2", "a_b"} {
		if err := CheckWorkgroup(value); err != nil {
			t.Errorf("CheckWorkgroup(%q) = %v", value, err)
		}
	}
	for _, value := range []string{
		"", "a workgroup", "TOO-LONG-A-NETBIOS-NAME", "-lead", "WG\n[global]",
		"WG#comment",
	} {
		if err := CheckWorkgroup(value); err == nil {
			t.Errorf("CheckWorkgroup(%q) was accepted", value)
		}
	}
}

func TestCheckMinProtocol(t *testing.T) {
	for _, value := range append([]string{"smb3_11"}, MinProtocols...) {
		if err := CheckMinProtocol(value); err != nil {
			t.Errorf("CheckMinProtocol(%q) = %v", value, err)
		}
	}
	// A dialect Samba knows but this tool does not offer is still refused: the
	// picker is a closed set, and anything outside it arrived some other way.
	for _, value := range []string{"", "LANMAN1", "CORE", "SMB4", "NT1 SMB2"} {
		if err := CheckMinProtocol(value); err == nil {
			t.Errorf("CheckMinProtocol(%q) was accepted", value)
		}
	}
}

func TestCheckHostSpec(t *testing.T) {
	for _, value := range []string{
		"192.168.1.", "192.168.1.5", "192.168.1.0/24",
		"192.168.1.0/255.255.255.0", "fileserver", "fileserver.example.com",
		".example.com", "fe80::/10", "ALL", "EXCEPT", "local",
	} {
		if err := CheckHostSpec(value); err != nil {
			t.Errorf("CheckHostSpec(%q) = %v", value, err)
		}
	}
	for _, value := range []string{
		"", "192.168.1.5 ; rm", "host\n[global]", "host#comment", "a b",
		"[global]",
	} {
		if err := CheckHostSpec(value); err == nil {
			t.Errorf("CheckHostSpec(%q) was accepted", value)
		}
	}
}

func TestRenderGlobal(t *testing.T) {
	stanza, err := RenderGlobal(shares.GlobalRequest{
		Workgroup: "OFFICE", MinProtocol: "smb3_11",
		HostsAllow: "192.168.1., 127.",
	}, []string{"\tlog level = 1"})
	if err != nil {
		t.Fatalf("RenderGlobal: %v", err)
	}
	want := []string{
		"[global]",
		"\tworkgroup = OFFICE",
		"\tserver min protocol = SMB3_11",
		"\thosts allow = 192.168.1. 127.",
		// Whatever the drop-in already carried and this form has no field for.
		"\tlog level = 1",
	}
	if strings.Join(stanza, "\n") != strings.Join(want, "\n") {
		t.Errorf("stanza =\n%s\nwant\n%s", strings.Join(stanza, "\n"),
			strings.Join(want, "\n"))
	}

	// An empty host list is a real answer — everybody — so it is written as no
	// line at all rather than as an empty one.
	open, err := RenderGlobal(shares.GlobalRequest{
		Workgroup: "OFFICE", MinProtocol: "SMB2_02"}, nil)
	if err != nil {
		t.Fatalf("RenderGlobal: %v", err)
	}
	if strings.Contains(strings.Join(open, "\n"), "hosts allow") {
		t.Errorf("an empty host list was written:\n%s", strings.Join(open, "\n"))
	}
}

func TestRenderGlobalRefusesWhatWouldSmuggleADirective(t *testing.T) {
	base := shares.GlobalRequest{Workgroup: "OFFICE", MinProtocol: "SMB2_02"}

	workgroup := base
	workgroup.Workgroup = "OFFICE\n\tsecurity = share"
	if _, err := RenderGlobal(workgroup, nil); err == nil {
		t.Errorf("a workgroup carrying a second parameter was accepted")
	}

	protocol := base
	protocol.MinProtocol = "SMB2_02\n[public]"
	if _, err := RenderGlobal(protocol, nil); err == nil {
		t.Errorf("a protocol carrying a second section was accepted")
	}

	hosts := base
	hosts.HostsAllow = "192.168.1.5 host#comment"
	if _, err := RenderGlobal(hosts, nil); err == nil {
		t.Errorf("a host list entry with a comment character was accepted")
	}
}

// TestKeepGlobalLinesKeepsWhatTheFormHasNoFieldFor: the same rule as a share
// edit, so a drop-in somebody added a parameter to survives being re-saved.
func TestKeepGlobalLinesKeepsWhatTheFormHasNoFieldFor(t *testing.T) {
	kept := strings.Join(KeepGlobalLines([]string{
		"[global]",
		"\t# somebody's note",
		"\tworkgroup = WG",
		"\tServer Min Protocol = SMB2_02",
		"\thosts allow = 10.",
		"\tlog level = 3",
	}), "\n")
	for _, want := range []string{"# somebody's note", "log level = 3"} {
		if !strings.Contains(kept, want) {
			t.Errorf("KeepGlobalLines dropped %q:\n%s", want, kept)
		}
	}
	for _, unwanted := range []string{"workgroup =", "Server Min Protocol =",
		"hosts allow ="} {
		if strings.Contains(kept, unwanted) {
			t.Errorf("KeepGlobalLines kept a managed parameter %q:\n%s",
				unwanted, kept)
		}
	}
}

func TestRemoveInclude(t *testing.T) {
	line := IncludeLineFor("team")
	existing := "[global]\n\tworkgroup = WG\n\t" + line +
		"\n\n[public]\n\tpath = /srv/public\n"

	updated, err := RemoveInclude(existing, line)
	if err != nil {
		t.Fatalf("RemoveInclude: %v", err)
	}
	if strings.Contains(updated, line) {
		t.Errorf("the include line survived:\n%s", updated)
	}
	for _, want := range []string{"[global]", "workgroup = WG", "[public]",
		"/srv/public"} {
		if !strings.Contains(updated, want) {
			t.Errorf("RemoveInclude lost %q:\n%s", want, updated)
		}
	}

	// A file that does not have it is returned exactly as it is, so a plan
	// built on one carries no smb.conf change at all.
	again, err := RemoveInclude(updated, line)
	if err != nil {
		t.Fatalf("RemoveInclude: %v", err)
	}
	if again != updated {
		t.Errorf("removing an absent include changed the file")
	}
}

// TestCheckOwnedRefusesEveryShareThisToolDidNotWrite, one refusal at a time:
// each of them is a different way a share can be somebody else's, and each
// deserves its own sentence rather than one "cannot remove that".
func TestCheckOwnedRefusesEveryShareThisToolDidNotWrite(t *testing.T) {
	const configPath = ConfigPath
	dropIn := DropInFor("team")
	ours := Header + "[team]\n\tpath = /srv/team\n"
	reaching := "[global]\n\t" + IncludeLineFor("team") + "\n"

	tests := []struct {
		name     string
		main     string
		dropIn   string
		contains string
	}{
		{
			name:     "written in smb.conf itself",
			main:     "[global]\n\n[team]\n\tpath = /srv/team\n",
			dropIn:   ours,
			contains: "is written in " + configPath + " itself",
		},
		{
			name:     "no drop-in of ours at all",
			main:     reaching,
			dropIn:   "",
			contains: "defined somewhere tui-samba did not write",
		},
		{
			name:     "a file of the same name somebody else wrote",
			main:     reaching,
			dropIn:   "[team]\n\tpath = /srv/team\n",
			contains: "was not written by tui-samba",
		},
		{
			name:     "our file, but it does not define the share",
			main:     reaching,
			dropIn:   Header + "[other]\n\tpath = /srv/other\n",
			contains: "does not define [team]",
		},
		{
			name:     "our file, reached some other way",
			main:     "[global]\n\tworkgroup = WG\n",
			dropIn:   ours,
			contains: "does not include it",
		},
	}
	for _, test := range tests {
		err := checkOwned("team", configPath, test.main, dropIn, test.dropIn)
		if err == nil {
			t.Errorf("%s: the share was accepted for removal", test.name)
			continue
		}
		if !strings.Contains(err.Error(), test.contains) {
			t.Errorf("%s: error = %q, want it to say %q", test.name, err,
				test.contains)
		}
	}

	// And the one shape it does accept.
	if err := checkOwned("team", configPath, reaching, dropIn, ours); err != nil {
		t.Errorf("a share this tool wrote was refused: %v", err)
	}
}

// TestTheLabelIsOnlyForAnEnforcingPolicy: a chcon on a machine where SELinux
// is merely loaded changes a label nothing reads, and on one where it is
// disabled the command does not exist. Only "Enforcing" is the state where a
// missing label is what stops a client.
func TestTheLabelIsOnlyForAnEnforcingPolicy(t *testing.T) {
	r := &Real{}
	tests := []struct {
		state shares.SELinux
		want  bool
	}{
		{shares.SELinux{}, false},
		{shares.SELinux{Enabled: false, Mode: "Enforcing"}, false},
		{shares.SELinux{Enabled: true, Mode: "Permissive"}, false},
		{shares.SELinux{Enabled: true, Mode: "Disabled"}, false},
		{shares.SELinux{Enabled: true, Mode: ""}, false},
		{shares.SELinux{Enabled: true, Mode: "Enforcing"}, true},
		{shares.SELinux{Enabled: true, Mode: "enforcing\n"}, true},
	}
	for _, test := range tests {
		if got := r.enforcing(shares.Model{SELinux: test.state}); got != test.want {
			t.Errorf("enforcing(%+v) = %v, want %v", test.state, got, test.want)
		}
	}
}

func TestOwnerOrRoot(t *testing.T) {
	for value, want := range map[string]string{
		"": "root", "  ": "root", "alice": "alice", " alice ": "alice",
	} {
		if got := ownerOrRoot(value); got != want {
			t.Errorf("ownerOrRoot(%q) = %q, want %q", value, got, want)
		}
	}
}
