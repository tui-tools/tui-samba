package samba

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tui-tools/tui-kit/runner"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// Fake is an in-memory file server. It backs --demo and the tests: every key
// works, every command is built and previewed exactly as the real backend
// builds it, and nothing reaches the system.
//
// It is not a set of canned screens. The sample machine holds a real smb.conf
// and a real drop-in, and every read goes through the same parsers the real
// backend uses — the configuration through ParseTestparm with the `include`
// expanded the way testparm expands it, the accounts through ParsePdbedit, the
// connections through ParseStatusJSON on the document from Samba's own manual.
// So --demo exercises the parser rather than a shortcut around it, and an edit
// made in the demo produces the same diff it would produce on a real machine.
type Fake struct {
	// files is the sample machine's /etc: the server's configuration and
	// whatever this tool has written beside it.
	files map[string]string
	// dirs is the Unix side of the exported paths. It is data rather than a
	// stat, because the point of the sample machine is to hold the states a
	// real one is found in — including a directory that is not there.
	dirs  map[string]shares.DirInfo
	users []shares.User
	model shares.Model
	run   *runner.Fake
	now   func() time.Time
}

// demoHostname is the sample machine's name, which is what a client types.
const demoHostname = "fileserver"

// NewFake builds the sample machine: four shares in the states a real one is
// found in, three accounts one of which is disabled, and two clients connected
// with files open.
func NewFake() *Fake { return NewFakeAt(time.Now) }

// NewFakeAt builds the sample machine as it would look at one instant, which
// is what a test and a screenshot need.
func NewFakeAt(now func() time.Time) *Fake {
	f := &Fake{now: now}
	f.run = &runner.Fake{Prefix: "sudo -n", Hook: f.apply}
	f.reset()
	return f
}

// reset builds the sample machine. It is a function rather than a literal so
// --demo starts from the same machine every time, however it was left.
func (f *Fake) reset() {
	f.files = map[string]string{
		"/etc/samba/smb.conf":              demoSmbConf,
		"/etc/samba/tui-samba.d/team.conf": demoTeamConf,
	}
	f.dirs = map[string]shares.DirInfo{
		"/srv/public": {Path: "/srv/public", Exists: true, IsDir: true,
			Mode: "0755", Owner: "root", Group: "root"},
		// The one somebody fixed with chmod 777 when a client could not write,
		// which is the commonest permission mistake on a file server and the
		// one no Samba setting takes back.
		"/srv/team": {Path: "/srv/team", Exists: true, IsDir: true,
			Mode: "0777", Owner: "root", Group: "staff", WorldWritable: true},
		// The one whose disk was moved and whose share was left behind.
		"/srv/archive": {Path: "/srv/archive", Note: "no such directory"},
	}
	f.users = []shares.User{
		{Name: "alice", UID: 1000, Flags: "U", PasswordLastSet: demoPasswordSet,
			UnixPresent: true},
		{Name: "bob", UID: 1001, Flags: "U", PasswordLastSet: demoPasswordSet,
			UnixPresent: true},
		// The one who left, whose account was disabled rather than removed —
		// and which looks exactly like a working one until somebody tries it.
		{Name: "carol", UID: 1002, Flags: "DU", Disabled: true,
			PasswordLastSet: demoPasswordSet, UnixPresent: true},
	}
	f.rebuild()
}

// demoSmbConf is the sample machine's own configuration, written the way a
// distribution's is: a few lines somebody chose, in a file full of defaults.
const demoSmbConf = `# Samba on the sample machine.

[global]
	workgroup = WORKGROUP
	server string = %h file server
	security = USER
	map to guest = Bad User
	server min protocol = SMB2_02
	server max protocol = SMB3_11
	log file = /var/log/samba/log.%m
	include = /etc/samba/tui-samba.d/team.conf

[homes]
	comment = Home directories
	browseable = No
	read only = No
	valid users = %S

[public]
	comment = Read-only files for everyone on the network
	path = /srv/public
	browseable = Yes
	read only = Yes
	guest ok = Yes

[archive]
	comment = Last year's projects
	path = /srv/archive
	browseable = Yes
	read only = Yes
	valid users = @staff
`

// demoTeamConf is the drop-in tui-samba wrote on the sample machine, reached
// by the one `include` line in smb.conf.
const demoTeamConf = `# Written by tui-samba. This file is included from smb.conf.
[team]
	comment = Shared working files
	path = /srv/team
	browseable = yes
	read only = no
	guest ok = no
	valid users = alice bob
	write list = alice bob
	create mask = 0664
	directory mask = 2775
`

// demoPasswordSet is when the sample accounts last had a password set, in the
// format pdbedit prints.
const demoPasswordSet = "Mon, 03 Aug 2026 09:14:22 -03"

// demoStatusJSON is what `smbstatus --json` answers on the sample machine.
//
// The shape is Samba's own, from the example in the smbstatus manual page:
// two sessions, the shares they have open, and a file open under each. It goes
// through the real parser, so the connections screen is showing a document
// that was parsed rather than a struct that was filled in.
const demoStatusJSON = `{
  "timestamp": "2026-08-30T09:41:02.113402-0300",
  "version": "4.20.5",
  "smb_conf": "/etc/samba/smb.conf",
  "sessions": {
    "3639217376": {
      "session_id": "3639217376",
      "server_id": { "pid": "2841", "task_id": "0", "vnn": "4294967295",
                     "unique_id": "10756714984493602300" },
      "uid": 1000, "gid": 1000,
      "username": "alice", "groupname": "alice",
      "remote_machine": "192.168.1.50",
      "hostname": "ipv4:192.168.1.50:49731",
      "session_dialect": "SMB3_11",
      "encryption": { "cipher": "", "degree": "none" },
      "signing": { "cipher": "AES-128-GMAC", "degree": "partial" }
    },
    "1174490237": {
      "session_id": "1174490237",
      "server_id": { "pid": "2903", "task_id": "0", "vnn": "4294967295",
                     "unique_id": "10756714984493602301" },
      "uid": 65534, "gid": 65534,
      "username": "nobody", "groupname": "nobody",
      "remote_machine": "192.168.1.77",
      "hostname": "ipv4:192.168.1.77:52204",
      "session_dialect": "SMB3_11",
      "encryption": { "cipher": "AES-128-GMAC", "degree": "full" },
      "signing": { "cipher": "AES-128-GMAC", "degree": "partial" }
    }
  },
  "tcons": {
    "3813255619": {
      "service": "team",
      "server_id": { "pid": "2841", "task_id": "0", "vnn": "4294967295",
                     "unique_id": "10756714984493602300" },
      "tcon_id": "3813255619", "session_id": "3639217376",
      "machine": "192.168.1.50",
      "connected_at": "2026-08-30T08:12:44-0300",
      "encryption": { "cipher": "", "degree": "none" },
      "signing": { "cipher": "AES-128-GMAC", "degree": "partial" }
    },
    "2299104558": {
      "service": "public",
      "server_id": { "pid": "2903", "task_id": "0", "vnn": "4294967295",
                     "unique_id": "10756714984493602301" },
      "tcon_id": "2299104558", "session_id": "1174490237",
      "machine": "192.168.1.77",
      "connected_at": "2026-08-30T09:38:10-0300",
      "encryption": { "cipher": "AES-128-GMAC", "degree": "full" },
      "signing": { "cipher": "AES-128-GMAC", "degree": "partial" }
    }
  },
  "open_files": {
    "/srv/team/budget.ods": {
      "service_path": "/srv/team",
      "filename": "budget.ods",
      "fileid": { "devid": 59, "inode": 11404245, "extid": 0 },
      "num_pending_deletes": 0,
      "opens": {
        "2841/7": {
          "server_id": { "pid": "2841", "task_id": "0", "vnn": "4294967295",
                         "unique_id": "10756714984493602300" },
          "username": "alice", "uid": 1000, "share_file_id": 7,
          "sharemode": { "hex": "0x00000003", "READ": true, "WRITE": true,
                         "text": "RW" },
          "access_mask": { "hex": "0x00000003", "READ_DATA": true,
                           "WRITE_DATA": true, "text": "RW" },
          "caching": { "READ": false, "WRITE": false, "HANDLE": false,
                       "hex": "0x00000000", "text": "" },
          "oplock": { "text": "NONE" },
          "lease": {},
          "opened_at": "2026-08-30T08:13:02-0300"
        }
      }
    },
    "/srv/public/handbook.pdf": {
      "service_path": "/srv/public",
      "filename": "handbook.pdf",
      "fileid": { "devid": 59, "inode": 11404912, "extid": 0 },
      "num_pending_deletes": 0,
      "opens": {
        "2903/3": {
          "server_id": { "pid": "2903", "task_id": "0", "vnn": "4294967295",
                         "unique_id": "10756714984493602301" },
          "username": "nobody", "uid": 65534, "share_file_id": 3,
          "sharemode": { "hex": "0x00000001", "READ": true, "text": "R" },
          "access_mask": { "hex": "0x00000001", "READ_DATA": true,
                           "text": "R" },
          "caching": { "READ": false, "WRITE": false, "HANDLE": false,
                       "hex": "0x00000000", "text": "" },
          "oplock": { "text": "LEVEL_II" },
          "lease": {},
          "opened_at": "2026-08-30T09:38:22-0300"
        }
      }
    }
  },
  "byte_range_locks": {
    "59/11404245/0": {
      "fileid": { "devid": 59, "inode": 11404245, "extid": 0 },
      "file_name": "budget.ods",
      "share_path": "/srv/team",
      "server_id": { "pid": "2841" },
      "locks": []
    }
  }
}`

// expand inlines the files an `include =` line points at, the way testparm
// does before it prints. It is what makes the drop-in visible to the parser,
// and it is the reason an edit in the demo behaves the way it will on a real
// machine.
func (f *Fake) expand(text string, depth int) string {
	if depth > 8 {
		return text
	}
	var out []string
	for _, line := range splitLines(text) {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && normalizeKey(key) == "include" {
			included, ok := f.files[strings.TrimSpace(value)]
			if !ok {
				continue
			}
			out = append(out, splitLines(f.expand(included, depth+1))...)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// rebuild runs the sample machine's files through the real pipeline: the same
// parser, the same judgement, the same sort.
func (f *Fake) rebuild() {
	model := shares.Model{
		Backend:   "samba",
		Installed: true,
		Version:   "4.20.5",
		Hostname:  demoHostname,
		Now:       f.now(),
	}

	banner := "Load smb config files from /etc/samba/smb.conf\n" +
		"Loaded services file OK.\nServer role: ROLE_STANDALONE\n\n"
	global, list := ParseTestparm(banner + f.expand(f.files["/etc/samba/smb.conf"], 0))
	global.Includes = ParseIncludes(f.files["/etc/samba/smb.conf"])

	for i := range list {
		if info, ok := f.dirs[list[i].Path]; ok {
			list[i].Dir = info
		} else if list[i].Path != "" {
			list[i].Dir = shares.DirInfo{Path: list[i].Path,
				Note: "no such directory"}
		}
		list[i] = JudgeShare(list[i], global)
	}
	global.Findings = JudgeGlobal(global, list)
	shares.SortShares(list)
	model.Global = global
	model.Shares = list

	users := append([]shares.User{}, f.users...)
	sort.SliceStable(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	for i := range users {
		users[i] = JudgeUser(users[i])
	}
	model.Users = users

	sessions, connections, files, err := ParseStatusJSON(demoStatusJSON)
	if err != nil {
		panic("samba: the demo status document does not parse: " + err.Error())
	}
	model.Sessions, model.TreeConnects = sessions, connections
	model.OpenFiles = attach(files, model.Shares)

	model.Services = []shares.Service{
		{Unit: "smb.service", Role: shares.RoleFileServer, Present: true,
			Enabled: true, Active: true, State: "active", Enablement: "enabled"},
		{Unit: "nmb.service", Role: shares.RoleNetBIOS, Present: true,
			State: "inactive", Enablement: "disabled"},
		{Unit: "winbind.service", Role: shares.RoleWinbind,
			Detail: "no unit called winbind.service or winbindd.service exists " +
				"on this machine"},
	}
	model.Ports = []shares.Port{
		{Port: 445, Address: "0.0.0.0", Process: "smbd"},
		{Port: 445, Address: "[::]", Process: "smbd"},
	}
	model.SELinux = shares.SELinux{}
	f.model = model
}

// Name identifies the backend. It is the real backend's name, because --demo
// shows what the real one would show.
func (f *Fake) Name() string { return "samba" }

// Describe says plainly that nothing here is real.
func (f *Fake) Describe() string { return "demo (an in-memory file server)" }

// Capabilities reports the same capabilities as a machine with the whole Samba
// suite installed, which is what the sample machine has.
func (f *Fake) Capabilities() shares.Capabilities {
	return shares.Capabilities{
		CanEditShares:  true,
		ConfigPath:     "/etc/samba/smb.conf",
		DropInDir:      DropInDir,
		CanManageUsers: true,
		CanReload:      true,
		CanSelfTest:    true,
		StatusJSON:     true,
	}
}

// Preview renders the command line the real backend would run.
func (f *Fake) Preview(cmd shares.Command) string { return f.run.Preview(cmd) }

// Load returns the sample machine.
func (f *Fake) Load(_ context.Context) (shares.Model, error) { return f.model, nil }

// Run records the command and applies its effect to the sample machine.
func (f *Fake) Run(ctx context.Context, cmd shares.Command) (string, error) {
	return f.run.Run(ctx, cmd)
}

// Ran exposes the recorded commands, which is what a test asserts on.
func (f *Fake) Ran() []shares.Command { return f.run.Ran }

// apply is the hook the fake runner calls: it makes to the in-memory machine
// the change the real command would have made, so the demo stays coherent as
// keys are pressed.
func (f *Fake) apply(cmd shares.Command) (string, error) {
	argv := cmd.Argv
	if len(argv) < 2 {
		return "", nil
	}
	switch argv[0] {
	case "install":
		return f.install(argv)
	case BinSmbpasswd:
		return f.passwd(argv)
	case BinSmbcontrol:
		return "", nil
	case BinSmbclient:
		return f.shareList(), nil
	case BinTestparm:
		return "Loaded services file OK.", nil
	}
	return "", nil
}

// install copies a staged file onto the sample machine, reading the real
// staged file the plan wrote. The demo therefore installs exactly the bytes
// the confirm dialog had checked.
func (f *Fake) install(argv []string) (string, error) {
	if len(argv) >= 4 && argv[1] == "-d" {
		// `install -d` creates the drop-in directory, which the in-memory
		// machine does not model: a file appearing in it is the whole effect.
		return "", nil
	}
	if len(argv) < 5 {
		return "", fmt.Errorf("install: %v is not a copy this demo understands", argv)
	}
	source, destination := argv[len(argv)-2], argv[len(argv)-1]
	body, err := os.ReadFile(source) //nolint:gosec // the path is the staging file this process just wrote
	if err != nil {
		return "", err
	}
	f.files[destination] = string(body)
	f.rebuild()
	return "", nil
}

// passwd applies an account change to the sample database.
func (f *Fake) passwd(argv []string) (string, error) {
	if len(argv) < 2 {
		return "", nil
	}
	name := argv[len(argv)-1]
	switch {
	case contains(argv, "-a"):
		f.users = append(f.users, shares.User{Name: name, UID: 1100,
			Flags: "U", PasswordLastSet: f.now().Format(
				"Mon, 02 Jan 2006 15:04:05 -0700"), UnixPresent: true})
	case contains(argv, "-x"):
		var kept []shares.User
		for _, user := range f.users {
			if user.Name != name {
				kept = append(kept, user)
			}
		}
		f.users = kept
	case contains(argv, "-d"):
		f.setFlags(name, "DU", true)
	case contains(argv, "-e"):
		f.setFlags(name, "U", false)
	default:
		f.setPasswordSet(name)
	}
	f.rebuild()
	return "", nil
}

// setFlags changes one account's flags on the sample machine.
func (f *Fake) setFlags(name, flags string, disabled bool) {
	for i := range f.users {
		if f.users[i].Name == name {
			f.users[i].Flags = flags
			f.users[i].Disabled = disabled
		}
	}
}

// setPasswordSet records that an account's password was replaced.
func (f *Fake) setPasswordSet(name string) {
	for i := range f.users {
		if f.users[i].Name == name {
			f.users[i].PasswordLastSet = f.now().Format(
				"Mon, 02 Jan 2006 15:04:05 -0700")
		}
	}
}

// shareList is what `smbclient -L` prints for the sample machine: the shares an
// anonymous client can see, which is the browseable ones and nothing else.
func (f *Fake) shareList() string {
	lines := []string{"", "\tSharename       Type      Comment",
		"\t---------       ----      -------"}
	for _, share := range f.model.Shares {
		if !share.Browseable {
			continue
		}
		lines = append(lines, "\t"+pad(share.Name, 16)+"Disk      "+share.Comment)
	}
	lines = append(lines, "\tIPC$            IPC       IPC Service", "")
	return strings.Join(lines, "\n")
}

// pad right-pads a column of the sample share list.
func pad(value string, width int) string {
	for len(value) < width {
		value += " "
	}
	return value
}

// contains reports whether an argv carries a flag.
func contains(argv []string, flag string) bool {
	for _, value := range argv {
		if value == flag {
			return true
		}
	}
	return false
}

// BuildShareWrite renders the same plan the real backend renders, against the
// sample machine's own files.
func (f *Fake) BuildShareWrite(_ context.Context, model shares.Model,
	req shares.ShareRequest) (shares.WritePlan, error) {
	if err := CheckShareName(req.Name); err != nil {
		return shares.WritePlan{}, err
	}
	if req.Original != "" && req.Original != req.Name {
		return shares.WritePlan{}, fmt.Errorf(
			"samba: tui-samba does not rename a share — create %s, then remove "+
				"[%s] by hand", req.Name, req.Original)
	}

	configPath := "/etc/samba/smb.conf"
	mainRaw := f.files[configPath]
	inMain := hasStanza(mainRaw, req.Name)
	destination := DropInFor(req.Name)
	if inMain {
		destination = configPath
	}
	before := f.files[destination]

	stanza, err := RenderShare(req, KeepLines(rawStanza(before, req.Name)))
	if err != nil {
		return shares.WritePlan{}, err
	}

	plan := shares.WritePlan{Path: destination}
	if inMain {
		plan.Title = "Rewrite [" + req.Name + "] in " + configPath
		content, replaceErr := ReplaceStanza(before, req.Name, stanza)
		if replaceErr != nil {
			return shares.WritePlan{}, replaceErr
		}
		plan.Content = content
		plan.Warning = "This share is defined in " + configPath +
			", so that is where it is changed. Every other line of the file is " +
			"left exactly as it is."
	} else {
		plan.Title = "Write [" + req.Name + "] to " + destination
		plan.Content = RenderDropIn(stanza)
	}
	plan.Diff = shares.Diff(destination, before, plan.Content)

	staged, err := Stage(filepath.Base(destination), plan.Content)
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan.TempPath = staged

	var commands []shares.Command
	install, err := BuildInstall(staged, destination)
	if err != nil {
		return shares.WritePlan{}, err
	}
	commands = append(commands, install)

	if !inMain {
		include := IncludeLineFor(req.Name)
		updated, includeErr := AddInclude(mainRaw, include)
		if includeErr != nil {
			return shares.WritePlan{}, includeErr
		}
		if updated != mainRaw {
			mainStaged, stageErr := Stage("smb.conf", updated)
			if stageErr != nil {
				return shares.WritePlan{}, stageErr
			}
			mainInstall, installErr := BuildInstall(mainStaged, configPath)
			if installErr != nil {
				return shares.WritePlan{}, installErr
			}
			commands = append(commands, mainInstall)
			plan.Diff += shares.Diff(configPath, mainRaw, updated)
			plan.Warning = "Samba reads a drop-in only where an `include` line " +
				"points at it, so one line is added to " + configPath +
				": `" + include + "`. Nothing else in that file changes."
		}
	}
	commands = append(commands, BuildReload())
	plan.Commands = commands

	// The sample machine answers the check the way a server with a readable
	// staged file does, because the file really was staged and really is
	// readable.
	validate, err := BuildValidate(staged)
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan.ValidationCommand = f.run.Preview(validate)
	plan.Validated = true
	plan.Validation = "Samba's own parser read the staged file: " +
		"Loaded services file OK."
	_ = model
	return plan, nil
}

// BuildUserAdd adds an account to the sample database.
func (f *Fake) BuildUserAdd(model shares.Model, name,
	password string) (shares.Command, error) {
	if _, exists := model.User(name); exists {
		return shares.Command{}, fmt.Errorf(
			"samba: %s is already in the Samba password database — press p to "+
				"set its password instead", name)
	}
	return BuildUserAdd(name, password)
}

// BuildUserAction is delete, enable and disable on the sample database.
func (f *Fake) BuildUserAction(_ shares.Model, name,
	action string) (shares.Command, error) {
	return BuildUserAction(name, action)
}

// BuildSetPassword replaces a sample account's password.
func (f *Fake) BuildSetPassword(_ shares.Model, name,
	password string) (shares.Command, error) {
	return BuildSetPassword(name, password)
}

// BuildReload tells the sample server to re-read its configuration.
func (f *Fake) BuildReload(_ shares.Model) (shares.Command, error) {
	return BuildReload(), nil
}

// BuildSelfTest asks the sample server for its share list.
func (f *Fake) BuildSelfTest(_ shares.Model) (shares.Command, error) {
	return BuildSelfTest(demoHostname)
}
