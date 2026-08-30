package samba

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tui-tools/tui-samba/internal/shares"
)

// fixture reads one recorded piece of output. Every parser in this package is
// covered by real output from a real program rather than by a string written
// to match the parser, which is the only way a parser test can fail when the
// program changes its mind.
func fixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // the name is a literal in the test above, and testdata is in the repository
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func TestParseTestparm(t *testing.T) {
	global, list := ParseTestparm(fixture(t, "testparm-s.txt"))

	if global.ConfigFile != "/etc/samba/smb.conf" {
		t.Errorf("ConfigFile = %q", global.ConfigFile)
	}
	if global.Workgroup != "WORKGROUP" || global.Security != "USER" {
		t.Errorf("global = %+v", global)
	}
	if global.MapToGuest != "Bad User" {
		t.Errorf("MapToGuest = %q", global.MapToGuest)
	}
	// This server was set back to NT1 by hand, which is the finding.
	if global.MinProtocol != "NT1" || !global.SMB1Enabled {
		t.Errorf("min protocol = %q, smb1 = %v", global.MinProtocol,
			global.SMB1Enabled)
	}
	// A parameter with a colon in its name is Samba's own idmap syntax and
	// must survive the key normalisation rather than being cut in half.
	if got := global.Params["idmap config * : backend"]; got != "tdb" {
		t.Errorf("idmap parameter = %q", got)
	}

	if len(list) != 4 {
		t.Fatalf("parsed %d shares, want 4: %+v", len(list), list)
	}
	byName := map[string]shares.Share{}
	for _, share := range list {
		byName[share.Name] = share
	}

	public := byName["public"]
	if !public.GuestOK || public.ReadOnly {
		t.Errorf("public = %+v", public)
	}
	if public.Path != "/srv/public" {
		t.Errorf("public path = %q", public.Path)
	}

	team := byName["team"]
	if team.ReadOnly {
		t.Errorf("team is writable in this configuration")
	}
	if got := strings.Join(team.ValidUsers, ","); got != "alice,bob" {
		t.Errorf("team valid users = %q", got)
	}
	if got := strings.Join(team.VFSObjects, ","); got != "recycle" {
		t.Errorf("team vfs objects = %q", got)
	}
	if team.CreateMask != "0664" || team.DirectoryMask != "2775" {
		t.Errorf("team masks = %q %q", team.CreateMask, team.DirectoryMask)
	}

	// [homes] and [printers] are Samba's own sections, and the model has to
	// say so: the editor refuses them and the screen explains why.
	if !byName["homes"].Special || !byName["printers"].Special {
		t.Errorf("the special stanzas were not recognised")
	}
	if byName["homes"].Browseable {
		t.Errorf("homes is browseable = No in this configuration")
	}
	if !byName["homes"].InheritPermissions {
		t.Errorf("homes sets inherit permissions in this configuration")
	}
}

// TestShareInheritsFromGlobal: testparm prints only what differs from the
// default, so a share that says nothing about `guest ok` is on whatever
// [global] says — and reading it as "not set, so no" would be wrong in the one
// direction that matters.
func TestShareInheritsFromGlobal(t *testing.T) {
	_, list := ParseTestparm("[global]\n\tguest ok = Yes\n\n[data]\n\tpath = /srv/data\n")
	if len(list) != 1 {
		t.Fatalf("parsed %d shares", len(list))
	}
	if !list[0].GuestOK {
		t.Errorf("the share did not inherit `guest ok` from [global]")
	}
}

// TestWriteableIsReadOnlyBackwards: `read only` and `writeable` are one
// parameter with two spellings and opposite senses, and a configuration may
// carry either.
func TestWriteableIsReadOnlyBackwards(t *testing.T) {
	for _, test := range []struct {
		line string
		want bool
	}{
		{"\tread only = No", false},
		{"\twriteable = Yes", false},
		{"\twritable = yes", false},
		{"\tread only = Yes", true},
		{"\twriteable = No", true},
	} {
		_, list := ParseTestparm("[data]\n\tpath = /srv/data\n" + test.line + "\n")
		if got := list[0].ReadOnly; got != test.want {
			t.Errorf("%q: ReadOnly = %v, want %v", test.line, got, test.want)
		}
	}
}

func TestIsSMB1(t *testing.T) {
	for value, want := range map[string]bool{
		"":         false, // the default since 4.11, which is SMB2
		"NT1":      true,
		"LANMAN2":  true,
		"CORE":     true,
		"SMB2":     false,
		"SMB2_02":  false,
		"SMB3":     false,
		"SMB3_11":  false,
		"nonsense": false,
	} {
		if got := IsSMB1(value); got != want {
			t.Errorf("IsSMB1(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestParseIncludes(t *testing.T) {
	raw := "[global]\n\t# include = /etc/samba/commented.conf\n" +
		"\tinclude = /etc/samba/tui-samba.d/team.conf\n\tworkgroup = WG\n"
	got := ParseIncludes(raw)
	if len(got) != 1 || got[0] != "/etc/samba/tui-samba.d/team.conf" {
		t.Errorf("ParseIncludes = %v", got)
	}
}

func TestParseServerVersion(t *testing.T) {
	if got := ParseServerVersion(fixture(t, "smbd-version.txt")); got != "4.20.5" {
		t.Errorf("ParseServerVersion = %q", got)
	}
}

func TestParsePdbedit(t *testing.T) {
	list := ParsePdbedit(fixture(t, "pdbedit-lv.txt"))
	if len(list) != 3 {
		t.Fatalf("parsed %d accounts, want 3: %+v", len(list), list)
	}

	alice := list[0]
	if alice.Name != "alice" || alice.Flags != "U" {
		t.Errorf("alice = %+v", alice)
	}
	if alice.Disabled || alice.NoPassword {
		t.Errorf("alice is an ordinary enabled account: %+v", alice)
	}
	if alice.PasswordLastSet != "Mon, 03 Aug 2026 09:14:22 -03" {
		t.Errorf("alice password last set = %q", alice.PasswordLastSet)
	}
	if alice.HomeDirectory != `\\fileserver\alice` {
		t.Errorf("alice home = %q", alice.HomeDirectory)
	}

	// The flags are the point of reading the verbose form: a disabled account
	// looks exactly like a working one until somebody tries it.
	if !list[1].Disabled {
		t.Errorf("carol carries [DU] and was not read as disabled: %+v", list[1])
	}
	if !list[2].NoPassword {
		t.Errorf("svc-backup carries [NU] and needs no password: %+v", list[2])
	}
}

func TestParseStatusJSON(t *testing.T) {
	sessions, connections, files, err := ParseStatusJSON(
		fixture(t, "smbstatus.json"))
	if err != nil {
		t.Fatalf("ParseStatusJSON: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("parsed %d sessions", len(sessions))
	}
	session := sessions[0]
	if session.PID != "69650" || session.User != "johndoe" {
		t.Errorf("session = %+v", session)
	}
	if session.Protocol != "SMB3_11" {
		t.Errorf("dialect = %q", session.Protocol)
	}
	// An empty cipher with a degree is how Samba spells "not encrypted", and
	// the screen has to show the word rather than a blank cell.
	if session.Encryption != "none" {
		t.Errorf("encryption = %q, want the degree when there is no cipher",
			session.Encryption)
	}
	if session.Signing != "AES-128-GMAC" {
		t.Errorf("signing = %q", session.Signing)
	}

	if len(connections) != 1 || connections[0].Service != "sharename" {
		t.Fatalf("connections = %+v", connections)
	}
	if connections[0].Encryption != "AES-128-GMAC" {
		t.Errorf("a share can encrypt when the session does not: %+v",
			connections[0])
	}

	if len(files) != 1 {
		t.Fatalf("parsed %d open files", len(files))
	}
	file := files[0]
	if file.Path != "/home/johndoe/testfolder/sample" {
		t.Errorf("path = %q", file.Path)
	}
	if file.Access != "RW" {
		t.Errorf("access = %q", file.Access)
	}
	// The byte-range lock table is keyed on the file, and a lock is what turns
	// "open" into "in use" on the screen.
	if !file.Locked {
		t.Errorf("the byte-range lock on this file was not read")
	}
	// Samba 4.17 reports a uid and no username for an open file, and a blank
	// column is the honest answer there.
	if file.User != "" {
		t.Errorf("user = %q, want empty on a 4.17 document", file.User)
	}
}

func TestParseStatusJSONRefusesRubbish(t *testing.T) {
	if _, _, _, err := ParseStatusJSON("smbstatus: no such option\n"); err == nil {
		t.Errorf("output with no JSON document in it was accepted")
	}
}

func TestParseStatusText(t *testing.T) {
	sessions, connections, files := ParseStatusText(
		fixture(t, "smbstatus-text.txt"))

	if len(sessions) != 2 {
		t.Fatalf("parsed %d sessions: %+v", len(sessions), sessions)
	}
	// Sorted by account, so nobody sorts after alice.
	if sessions[0].User != "alice" || sessions[0].PID != "2841" {
		t.Errorf("session = %+v", sessions[0])
	}
	// The machine column carries a name, a space and a bracketed address, and
	// splitting it on whitespace would cut it in half.
	if sessions[0].Machine != "192.168.1.50" {
		t.Errorf("machine = %q", sessions[0].Machine)
	}
	if sessions[0].Remote != "ipv4:192.168.1.50:49731" {
		t.Errorf("remote = %q", sessions[0].Remote)
	}
	if sessions[0].Protocol != "SMB3_11" ||
		sessions[0].Signing != "AES-128-GMAC" {
		t.Errorf("session = %+v", sessions[0])
	}

	if len(connections) != 2 {
		t.Fatalf("parsed %d connections: %+v", len(connections), connections)
	}
	if connections[0].Service != "public" || connections[0].PID != "2903" {
		t.Errorf("connection = %+v", connections[0])
	}

	if len(files) != 1 {
		t.Fatalf("parsed %d locked files: %+v", len(files), files)
	}
	if files[0].Path != "/srv/team/budget.ods" {
		t.Errorf("path = %q", files[0].Path)
	}
	if files[0].Oplock != "LEVEL_II" {
		t.Errorf("oplock = %q", files[0].Oplock)
	}
}

func TestParseListening(t *testing.T) {
	ports := ParseListening(fixture(t, "ss-tlnp.txt"))
	if len(ports) != 3 {
		t.Fatalf("kept %d ports, want the three SMB ones: %+v", len(ports), ports)
	}
	for _, port := range ports {
		if port.Port != 445 && port.Port != 139 {
			t.Errorf("a port that is not a file server's was kept: %+v", port)
		}
		if port.Process != "smbd" {
			t.Errorf("process = %q", port.Process)
		}
	}
	// An IPv6 address is bracketed and full of colons, and the port is after
	// the last one.
	var sawV6 bool
	for _, port := range ports {
		if port.Address == "[::]" {
			sawV6 = true
		}
	}
	if !sawV6 {
		t.Errorf("the IPv6 listener was not read: %+v", ports)
	}
}

func TestParseProperties(t *testing.T) {
	properties := ParseProperties(fixture(t, "systemctl-show-smb.txt"))
	if properties["ActiveState"] != "active" ||
		properties["UnitFileState"] != "enabled" {
		t.Errorf("properties = %v", properties)
	}
}

func TestParseBooleans(t *testing.T) {
	values := ParseBooleans(fixture(t, "getsebool.txt"))
	if values["samba_export_all_rw"] || !values["samba_export_all_ro"] {
		t.Errorf("booleans = %v", values)
	}
}

func TestParseStat(t *testing.T) {
	info, err := ParseStat(fixture(t, "stat-share.txt"))
	if err != nil {
		t.Fatalf("ParseStat: %v", err)
	}
	if info.Mode != "0777" || info.Owner != "root" || info.Group != "staff" {
		t.Errorf("info = %+v", info)
	}
	if !info.WorldWritable || !info.IsDir || !info.Exists {
		t.Errorf("a 0777 directory: %+v", info)
	}

	if _, err := ParseStat("stat: cannot statx '/srv/x'"); err == nil {
		t.Errorf("an error message was read as a mode")
	}
}

func TestParseContext(t *testing.T) {
	if got := ParseContext(fixture(t, "ls-z.txt")); got !=
		"system_u:object_r:samba_share_t:s0" {
		t.Errorf("ParseContext = %q", got)
	}
	if got := ParseContext("? /srv/public\n"); got != "" {
		t.Errorf("a machine without SELinux answered %q", got)
	}
}

func TestPadMode(t *testing.T) {
	for value, want := range map[string]string{
		"755": "0755", "0755": "0755", "2775": "2775", "7": "0007",
	} {
		if got := PadMode(value); got != want {
			t.Errorf("PadMode(%q) = %q, want %q", value, got, want)
		}
	}
}
