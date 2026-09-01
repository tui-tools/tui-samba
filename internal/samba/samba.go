// Package samba is the file-sharing backend of tui-samba, and the only place
// in the repository that starts a process.
//
// The programs driven, each through its own runner:
//
//	smbd         asked its version, and nothing else — the server is never
//	             started, stopped or restarted by this tool
//	testparm     the effective configuration, and the check on a staged one
//	pdbedit      the Samba password database, read
//	smbpasswd    the Samba password database, changed
//	smbstatus    sessions, connections and open files
//	smbcontrol   telling the running server to re-read its configuration
//	smbclient    the self-test: the share list as a client sees it
//	systemctl    whether the units are enabled and running
//	ss           whether anything is actually listening on 445 and 139
//	install      writing a staged file to its destination, and creating the
//	             directory a new share exports
//	rm           removing a drop-in this tool wrote, and nothing else
//	chcon        labelling a new share directory where SELinux is enforcing
//	getenforce   whether SELinux is enforcing
//	getsebool    the two booleans that decide whether Samba may export a
//	             directory at all
//
// Three more — `cat`, `stat` and `ls` — are the escalated fallbacks for a file
// or a directory an ordinary user cannot open. They are tried only after the
// plain Go read has already been refused.
//
// This is a file server tool. Samba can also be an Active Directory domain
// controller, and none of that is here: no `samba-tool`, no domain, no DNS, no
// replication. A domain controller is a different program with a different
// database and a different set of ways to break, and a tool that pretended to
// cover both would cover neither.
package samba

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/runner"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// ErrNotAvailable reports that a program this backend wanted is not installed.
var ErrNotAvailable = runner.ErrNotAvailable

// searchPaths are the locations a non-root PATH commonly omits. `smbd` is the
// one that matters: it lives in /usr/sbin on every distribution, which is not
// on an ordinary user's PATH, so without this the version probe would report
// "not installed" on a machine running a file server.
var searchPaths = map[string][]string{
	BinSmbd:       {"/usr/sbin/smbd", "/sbin/smbd", "/usr/local/sbin/smbd"},
	BinTestparm:   {"/usr/bin/testparm", "/bin/testparm", "/usr/sbin/testparm"},
	BinPdbedit:    {"/usr/bin/pdbedit", "/bin/pdbedit", "/usr/sbin/pdbedit"},
	BinSmbpasswd:  {"/usr/bin/smbpasswd", "/bin/smbpasswd", "/usr/sbin/smbpasswd"},
	BinSmbstatus:  {"/usr/bin/smbstatus", "/bin/smbstatus", "/usr/sbin/smbstatus"},
	BinSmbcontrol: {"/usr/bin/smbcontrol", "/bin/smbcontrol", "/usr/sbin/smbcontrol"},
	BinSmbclient:  {"/usr/bin/smbclient", "/bin/smbclient"},
	"systemctl":   {"/usr/bin/systemctl", "/bin/systemctl"},
	"ss":          {"/usr/bin/ss", "/bin/ss", "/usr/sbin/ss", "/sbin/ss"},
	"install":     {"/usr/bin/install", "/bin/install"},
	"rm":          {"/usr/bin/rm", "/bin/rm"},
	"chcon":       {"/usr/bin/chcon", "/bin/chcon"},
	"cat":         {"/usr/bin/cat", "/bin/cat"},
	"stat":        {"/usr/bin/stat", "/bin/stat"},
	"ls":          {"/usr/bin/ls", "/bin/ls"},
	"getenforce":  {"/usr/sbin/getenforce", "/sbin/getenforce", "/usr/bin/getenforce"},
	"getsebool":   {"/usr/sbin/getsebool", "/sbin/getsebool", "/usr/bin/getsebool"},
}

// The units a file server runs as, in the order they are asked about. The
// names are not the same everywhere — Fedora and Arch ship `smb.service`,
// Debian and Ubuntu ship `smbd.service` — so all of them are asked and the
// first one systemd knows is the answer.
var (
	fileServerUnits = []string{"smb.service", "smbd.service"}
	netbiosUnits    = []string{"nmb.service", "nmbd.service"}
	winbindUnits    = []string{"winbind.service", "winbindd.service"}
)

// sambaBooleans are the SELinux switches that decide whether Samba may export
// an ordinary directory at all. On Fedora and RHEL a share can be right in
// smb.conf, right in its Unix modes, and still refuse every client because of
// these — and nothing Samba prints says so.
var sambaBooleans = []string{"samba_export_all_rw", "samba_export_all_ro"}

// selinuxFS is the mount point whose presence means a policy is loaded.
const selinuxFS = "/sys/fs/selinux"

// Options are the settings the tool passes down from its configuration.
type Options struct {
	// ConfigPath overrides where smb.conf is looked for. It is a field
	// because a machine can be told to read another file, and because a test
	// needs one that is not the real machine's.
	ConfigPath string
}

// Real reads and changes the file server on this host. It satisfies
// shares.Backend.
type Real struct {
	smbd       *runner.Runner
	testparm   *runner.Runner
	pdbedit    *runner.Runner
	smbpasswd  *runner.Runner
	smbstatus  *runner.Runner
	smbcontrol *runner.Runner
	smbclient  *runner.Runner
	systemctl  *runner.Runner
	ss         *runner.Runner
	install    *runner.Runner
	rm         *runner.Runner
	chcon      *runner.Runner
	getenforce *runner.Runner
	getsebool  *runner.Runner
	// cat, stat and ls are the escalated fallbacks for a path an unprivileged
	// process cannot open.
	cat  *runner.Runner
	stat *runner.Runner
	ls   *runner.Runner

	// caps gates what only exists on a new enough server. It comes from the
	// manifest, so no version number is written into this file.
	caps compat.Caps
	opts Options
	// now is a field so a test and a screenshot read the same instant.
	now func() time.Time
	// lookup resolves a Unix account, so a test does not depend on whose
	// machine it runs on.
	lookup func(name string) bool
}

// Available reports whether this machine has a Samba file server to drive.
func Available() bool {
	return runner.Available(BinTestparm, searchPaths[BinTestparm]...) ||
		runner.Available(BinSmbd, searchPaths[BinSmbd]...)
}

// NewReal locates the programs and, when not running as root, validates the
// configured privilege prefix.
//
// Nothing here is required. A machine with no Samba at all still starts the
// tool, which says so on its first screen: "no file server is installed here"
// is an answer, and it is the one a machine that never had one deserves.
func NewReal(sudoPrefix []string, caps compat.Caps, opts Options) (*Real, error) {
	real := &Real{caps: caps, opts: opts, now: time.Now, lookup: unixUserExists}

	// The reads that need no privilege at all: a version, a systemd property,
	// a listening socket, an SELinux boolean.
	unprivileged := false
	for _, spec := range []struct {
		bin    string
		target **runner.Runner
		reads  *bool
	}{
		{BinSmbd, &real.smbd, &unprivileged},
		{BinTestparm, &real.testparm, &unprivileged},
		{BinPdbedit, &real.pdbedit, nil},
		{BinSmbpasswd, &real.smbpasswd, nil},
		{BinSmbstatus, &real.smbstatus, nil},
		{BinSmbcontrol, &real.smbcontrol, nil},
		{BinSmbclient, &real.smbclient, &unprivileged},
		{"systemctl", &real.systemctl, &unprivileged},
		{"ss", &real.ss, &unprivileged},
		{"install", &real.install, nil},
		{"rm", &real.rm, nil},
		{"chcon", &real.chcon, nil},
		{"getenforce", &real.getenforce, &unprivileged},
		{"getsebool", &real.getsebool, &unprivileged},
		{"cat", &real.cat, nil},
		{"stat", &real.stat, nil},
		{"ls", &real.ls, nil},
	} {
		r, err := runner.New(runner.Options{
			Bin:             spec.bin,
			SearchPaths:     searchPaths[spec.bin],
			SudoPrefix:      sudoPrefix,
			PrivilegedReads: spec.reads,
		})
		if err != nil {
			continue
		}
		*spec.target = r
	}
	return real, nil
}

// Name identifies the backend. The manifest declares one block, `samba`, for
// the whole suite: the programs ship together and carry one version between
// them.
func (r *Real) Name() string { return "samba" }

// Describe names the backend for the header: what is here to read with, and
// what is missing.
func (r *Real) Describe() string {
	if r.testparm == nil && r.smbd == nil {
		return "no Samba server is installed on this machine"
	}
	parts := []string{"samba"}
	if r.testparm == nil {
		parts = append(parts, "no testparm, so the configuration cannot be read")
	}
	if r.smbstatus == nil {
		parts = append(parts, "no smbstatus, so no connections")
	}
	if r.pdbedit == nil {
		parts = append(parts, "no pdbedit, so no accounts")
	}
	return strings.Join(parts, "; ")
}

// configPath is the file the server reads: what it named itself, what the
// configuration says, or the family default.
func (r *Real) configPath() string {
	if r.opts.ConfigPath != "" {
		return r.opts.ConfigPath
	}
	return ConfigPath
}

// Capabilities reports what this backend supports, which is a question about
// what is installed rather than a constant.
func (r *Real) Capabilities() shares.Capabilities {
	caps := shares.Capabilities{
		ConfigPath: r.configPath(),
		DropInDir:  DropInDir,
	}
	switch {
	case r.testparm == nil:
		caps.EditReason = "testparm is not installed, so nothing here can check " +
			"a configuration before it is written — and tui-samba does not " +
			"install one it has not had checked"
	case r.install == nil:
		caps.EditReason = "the `install` command was not found, so a staged " +
			"file cannot be put in place with its mode set in the same call"
	default:
		caps.CanEditShares = true
	}
	if r.smbpasswd == nil {
		caps.UsersReason = "smbpasswd is not installed, so the Samba password " +
			"database cannot be changed here"
	} else {
		caps.CanManageUsers = true
	}
	caps.CanReload = r.smbcontrol != nil
	caps.CanSelfTest = r.smbclient != nil
	caps.StatusJSON = r.smbstatus != nil && r.caps.Has(FeatureStatusJSON)
	return caps
}

// Preview renders the exact command line Run will execute. Every command goes
// through the runner of its own binary, so the preview carries the privilege
// prefix that binary will really be called with.
func (r *Real) Preview(cmd shares.Command) string {
	if run := r.runnerFor(cmd); run != nil {
		return run.Preview(cmd)
	}
	return cmd.String()
}

// runnerFor picks the runner that owns a command, by its argv[0].
func (r *Real) runnerFor(cmd shares.Command) *runner.Runner {
	if len(cmd.Argv) == 0 {
		return nil
	}
	switch cmd.Argv[0] {
	case BinTestparm:
		return r.testparm
	case BinSmbpasswd:
		return r.smbpasswd
	case BinSmbcontrol:
		return r.smbcontrol
	case BinSmbclient:
		return r.smbclient
	case "install":
		return r.install
	case "rm":
		return r.rm
	case "chcon":
		return r.chcon
	default:
		return nil
	}
}

// Run executes a previewed command.
func (r *Real) Run(ctx context.Context, cmd shares.Command) (string, error) {
	run := r.runnerFor(cmd)
	if run == nil {
		name := "(empty command)"
		if len(cmd.Argv) > 0 {
			name = cmd.Argv[0]
		}
		return "", fmt.Errorf("samba: %q is not available on this machine", name)
	}
	return run.Run(ctx, cmd)
}

// readEscalating makes a read twice: plainly first, and through the privilege
// prefix only when the plain one was refused.
//
// It is how the configuration is read on a machine where smb.conf is
// world-readable — which is nearly all of them — without asking for a
// privilege that is not needed, and still works on one where somebody tightened
// the mode. The escalated retry is a `Run` on the same runner, which is the
// runner's own word for "with the prefix".
func (r *Real) readEscalating(ctx context.Context, run *runner.Runner,
	argv ...string) (string, error) {
	if run == nil {
		return "", ErrNotAvailable
	}
	out, err := run.Read(ctx, argv...)
	if err == nil {
		return out, nil
	}
	if !run.Privileged() {
		return out, err
	}
	escalated, escalatedErr := run.Run(ctx, shares.Command{Argv: argv})
	if escalatedErr != nil {
		// The first message is the better one: it is what an ordinary user
		// would have been told.
		return out, err
	}
	return escalated, nil
}

// Load reads the file server's state.
//
// It never fails. A machine with no Samba is a real machine and "there is no
// file server here" is the true answer for it; a read this user is not allowed
// to make is recorded with its reason on the screen it belongs to rather than
// taking the whole load down. The error in the signature is the interface's.
func (r *Real) Load(ctx context.Context) (shares.Model, error) {
	model := shares.Model{Backend: r.Name(), Now: r.now()}
	if name, err := os.Hostname(); err == nil {
		model.Hostname = name
	}

	// The units and the ports are read whatever else is here, so the shape of
	// a report does not change between a machine with Samba and one without:
	// a script gets the same fields either way, and a machine with the units
	// but no binaries — a package half removed — is visible rather than
	// silently empty.
	model.Services = r.loadServices(ctx)
	model.Ports, model.PortsDetail = r.loadPorts(ctx)

	if r.testparm == nil && r.smbd == nil {
		model.Detail = "no Samba server is installed on this machine: neither " +
			"testparm nor smbd was found"
		return model, nil
	}
	model.Installed = true
	if r.smbd != nil {
		if out, err := r.smbd.Read(ctx, BinSmbd, "--version"); err == nil {
			model.Version = ParseServerVersion(out)
		}
	}

	model.SELinux = r.loadSELinux(ctx)
	r.loadConfig(ctx, &model)
	r.loadUsers(ctx, &model)
	r.loadStatus(ctx, &model)
	return model, nil
}

// loadConfig reads the effective configuration and the raw file behind it.
func (r *Real) loadConfig(ctx context.Context, model *shares.Model) {
	if r.testparm == nil {
		model.ConfigDetail = "testparm is not installed, so the effective " +
			"configuration cannot be read"
		return
	}
	out, err := r.readEscalating(ctx, r.testparm, BinTestparm, "-s")
	if err != nil {
		model.ConfigDetail = "`testparm -s` could not be run: " +
			runner.FirstLine(err.Error())
		return
	}
	global, list := ParseTestparm(out)
	if global.ConfigFile == "" {
		global.ConfigFile = r.configPath()
	}
	// The includes are the one thing testparm cannot report, because it
	// expands them before it prints. They come from the raw file.
	if raw, rawErr := r.readFile(ctx, global.ConfigFile); rawErr == nil {
		global.Includes = ParseIncludes(raw)
	}

	for i := range list {
		list[i].Dir = r.inspect(ctx, list[i], model.SELinux)
		list[i] = JudgeShare(list[i], global)
	}
	global.Findings = JudgeGlobal(global, list)
	shares.SortShares(list)

	model.Global = global
	model.Shares = list
}

// inspect is the Unix side of one share's path: does it exist, what mode is
// it, who owns it, and what SELinux label does it carry.
func (r *Real) inspect(ctx context.Context, share shares.Share,
	selinux shares.SELinux) shares.DirInfo {
	if share.Path == "" {
		return shares.DirInfo{}
	}
	info := shares.DirInfo{Path: share.Path}

	stat, err := os.Stat(share.Path)
	switch {
	case err == nil:
		info.Exists = true
		info.IsDir = stat.IsDir()
		mode := stat.Mode().Perm()
		info.Mode = PadMode(strconv.FormatUint(uint64(mode), 8))
		info.WorldWritable = mode&0o002 != 0
		info.Owner, info.Group = ownerOf(stat)
	case os.IsPermission(err) && r.stat != nil:
		// A path inside a directory this user cannot traverse: ask through the
		// privilege prefix rather than reporting a share as missing when it is
		// merely out of reach.
		out, statErr := r.stat.Read(ctx, "stat", "-c", "%a %U %G %F", "--",
			share.Path)
		if statErr != nil {
			info.Note = runner.FirstLine(statErr.Error())
			return info
		}
		escalated, parseErr := ParseStat(out)
		if parseErr != nil {
			info.Note = parseErr.Error()
			return info
		}
		escalated.Path = share.Path
		info = escalated
	case os.IsNotExist(err):
		info.Note = "no such directory"
	default:
		info.Note = runner.FirstLine(err.Error())
	}

	if info.Exists && selinux.Enabled && r.ls != nil {
		if out, lsErr := r.ls.Read(ctx, "ls", "-Zd", "--", share.Path); lsErr == nil {
			info.SELinuxContext = ParseContext(out)
		}
	}
	return info
}

// loadSELinux reads the policy state and the two Samba booleans.
func (r *Real) loadSELinux(ctx context.Context) shares.SELinux {
	state := shares.SELinux{}
	if _, err := os.Stat(selinuxFS); err != nil {
		return state
	}
	state.Enabled = true
	if r.getenforce != nil {
		if out, err := r.getenforce.Read(ctx, "getenforce"); err == nil {
			state.Mode = strings.TrimSpace(firstLine(out))
			state.Enabled = !strings.EqualFold(state.Mode, "disabled")
		}
	}
	if r.getsebool == nil {
		state.Detail = "getsebool is not installed, so the samba_export " +
			"booleans could not be read"
		return state
	}
	out, err := r.getsebool.Read(ctx,
		append([]string{"getsebool"}, sambaBooleans...)...)
	if err != nil {
		state.Detail = runner.FirstLine(err.Error())
		return state
	}
	state.Booleans = ParseBooleans(out)
	return state
}

// loadUsers reads the Samba password database.
//
// It is root-only on every distribution: the database lives beside the
// password hashes, and an ordinary user reading it would be an ordinary user
// reading those. An unprivileged run therefore gets an empty list with the
// reason, which is a different thing from a server with no accounts.
func (r *Real) loadUsers(ctx context.Context, model *shares.Model) {
	if r.pdbedit == nil {
		model.UsersDetail = "pdbedit is not installed, so the Samba password " +
			"database cannot be read"
		return
	}
	out, err := r.pdbedit.Read(ctx, BinPdbedit, "-L", "-v")
	if err != nil {
		model.UsersDetail = "`pdbedit -L -v` could not be run: " +
			runner.FirstLine(err.Error()) +
			" — the Samba password database is readable only by root"
		return
	}
	list := ParsePdbedit(out)
	for i := range list {
		list[i].UnixPresent = r.lookup(list[i].Name)
		if list[i].UnixPresent {
			list[i].UID = unixUID(list[i].Name)
		}
		list[i] = JudgeUser(list[i])
	}
	model.Users = list
}

// loadStatus reads the sessions, the connections and the open files.
func (r *Real) loadStatus(ctx context.Context, model *shares.Model) {
	if r.smbstatus == nil {
		model.StatusDetail = "smbstatus is not installed, so who is connected " +
			"cannot be read"
		return
	}

	if r.caps.Has(FeatureStatusJSON) {
		out, err := r.smbstatus.Read(ctx, BinSmbstatus, "--json")
		if err == nil {
			sessions, connections, files, parseErr := ParseStatusJSON(out)
			if parseErr == nil {
				model.Sessions, model.TreeConnects = sessions, connections
				model.OpenFiles = attach(files, model.Shares)
				return
			}
			model.StatusDetail = parseErr.Error() +
				" — falling back to the text output"
		}
	}

	out, err := r.smbstatus.Read(ctx, BinSmbstatus)
	if err != nil {
		model.StatusDetail = "`smbstatus` could not be run: " +
			runner.FirstLine(err.Error()) +
			" — it reads Samba's own databases, which are root-only"
		return
	}
	sessions, connections, files := ParseStatusText(out)
	model.Sessions, model.TreeConnects = sessions, connections
	model.OpenFiles = attach(files, model.Shares)
	if model.StatusDetail == "" && !r.caps.Has(FeatureStatusJSON) {
		since, _ := r.caps.Since(FeatureStatusJSON)
		model.StatusDetail = "this smbstatus has no --json (it arrived in Samba " +
			since + "), so the columns come from the text output and the " +
			"per-session encryption is whatever that carries"
	}
}

// attach names the share each open file is under.
//
// smbstatus reports the file's directory, not the share it was reached
// through, so the longest share path that is a prefix of it is the answer —
// longest, because a share nested inside another one is a real configuration
// and the inner one is the one the client used.
func attach(files []shares.OpenFile, list []shares.Share) []shares.OpenFile {
	for i, file := range files {
		best := ""
		for _, share := range list {
			if share.Path == "" || !under(file.Path, share.Path) {
				continue
			}
			if len(share.Path) > len(best) {
				best = share.Path
				files[i].Service = share.Name
			}
		}
	}
	return files
}

// under reports whether a file is inside a directory.
func under(file, dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return file == dir || strings.HasPrefix(file, dir+"/")
}

// loadServices reads the state of the units a file server runs as.
func (r *Real) loadServices(ctx context.Context) []shares.Service {
	roles := []struct {
		role  string
		units []string
	}{
		{shares.RoleFileServer, fileServerUnits},
		{shares.RoleNetBIOS, netbiosUnits},
		{shares.RoleWinbind, winbindUnits},
	}
	var list []shares.Service
	for _, entry := range roles {
		list = append(list, r.readService(ctx, entry.role, entry.units))
	}
	return list
}

// readService asks systemd about each candidate unit and keeps the first one
// it knows.
func (r *Real) readService(ctx context.Context, role string,
	units []string) shares.Service {
	service := shares.Service{Unit: units[0], Role: role}
	if r.systemctl == nil {
		service.Detail = "systemctl is not installed, so the unit state could " +
			"not be read"
		return service
	}
	for _, unit := range units {
		out, err := r.systemctl.Read(ctx, "systemctl", "show", unit,
			"--property=LoadState", "--property=ActiveState",
			"--property=UnitFileState")
		if err != nil {
			continue
		}
		properties := ParseProperties(out)
		if properties["LoadState"] == "not-found" {
			continue
		}
		return shares.Service{
			Unit:       unit,
			Role:       role,
			Present:    true,
			Active:     properties["ActiveState"] == "active",
			Enabled:    properties["UnitFileState"] == "enabled",
			State:      properties["ActiveState"],
			Enablement: properties["UnitFileState"],
		}
	}
	service.Detail = "no unit called " + strings.Join(units, " or ") +
		" exists on this machine"
	return service
}

// loadPorts reads what is listening on the two ports a file server answers on.
func (r *Real) loadPorts(ctx context.Context) ([]shares.Port, string) {
	if r.ss == nil {
		return nil, "the `ss` command was not found, so the listening sockets " +
			"could not be read"
	}
	// `-p` names the process holding a socket, which only root is shown. The
	// read is deliberately unprivileged: the ports themselves are the answer,
	// and a blank process column is a smaller loss than escalating for one.
	out, err := r.ss.Read(ctx, "ss", "-tlnp")
	if err != nil {
		return nil, runner.FirstLine(err.Error())
	}
	return ParseListening(out), ""
}

// readFile reads a file plainly, and through `cat` with the privilege prefix
// only when the plain read was refused.
func (r *Real) readFile(ctx context.Context, path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is the server's own configuration, named by the server
	if err == nil {
		return string(raw), nil
	}
	if !os.IsPermission(err) || r.cat == nil {
		return "", err
	}
	out, catErr := r.cat.Read(ctx, "cat", "--", path)
	if catErr != nil {
		return "", err
	}
	return out, nil
}

// ownerOf is the account and group names behind a file's ids, falling back to
// the numbers when there is no name for them.
func ownerOf(info os.FileInfo) (owner, group string) {
	uid, gid, ok := idsOf(info)
	if !ok {
		return "", ""
	}
	owner, group = strconv.FormatUint(uint64(uid), 10),
		strconv.FormatUint(uint64(gid), 10)
	if account, err := user.LookupId(owner); err == nil {
		owner = account.Username
	}
	if resolved, err := user.LookupGroupId(group); err == nil {
		group = resolved.Name
	}
	return owner, group
}

// unixUserExists reports whether a Unix account of that name is on the machine.
func unixUserExists(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

// unixUID is the numeric id behind an account name, -1 when there is none.
func unixUID(name string) int {
	account, err := user.Lookup(name)
	if err != nil {
		return -1
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return -1
	}
	return uid
}

// Stage writes a pending file to a private temporary directory and returns its
// path.
//
// Staging first is what makes a change reviewable and what makes the check
// meaningful: the file the user approves is a file that already exists, that
// Samba's own parser has already read, and that the install command copies
// byte for byte. Nothing reaches /etc until the confirmed commands run.
//
// The directory is created with MkdirTemp, so it is mode 0700 and owned by
// this process: a staged configuration cannot be swapped between the check and
// the install.
func Stage(name, content string) (string, error) {
	dir, err := os.MkdirTemp("", "tui-samba-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// BuildShareWrite stages the new configuration, has Samba's own parser check
// it, and returns the plan that installs it.
func (r *Real) BuildShareWrite(ctx context.Context, model shares.Model,
	req shares.ShareRequest) (shares.WritePlan, error) {
	caps := r.Capabilities()
	if !caps.CanEditShares {
		return shares.WritePlan{}, fmt.Errorf("%s", caps.EditReason)
	}
	plan, err := r.planShareWrite(ctx, model, req)
	if err != nil {
		return shares.WritePlan{}, err
	}
	r.validate(ctx, &plan)
	return plan, nil
}

// planShareWrite works out which file the change belongs in and what it will
// contain.
//
// The rule is one definition per share. A share already written in smb.conf is
// edited there; anything else — a new share, or one this tool wrote before —
// goes to a drop-in of its own. Writing a second definition of a share that
// already has one is the ambiguity nobody can debug afterwards, because which
// one wins depends on where the `include` line sits.
func (r *Real) planShareWrite(ctx context.Context, model shares.Model,
	req shares.ShareRequest) (shares.WritePlan, error) {
	if err := CheckShareName(req.Name); err != nil {
		return shares.WritePlan{}, err
	}
	if req.Original != "" && req.Original != req.Name {
		return shares.WritePlan{}, fmt.Errorf(
			"samba: tui-samba does not rename a share — a rename is two "+
				"changes, and the second one is deleting [%s], which is not "+
				"something to do behind a form. Create %s, then remove the old "+
				"one by hand", req.Original, req.Name)
	}

	configPath := model.Global.ConfigFile
	if configPath == "" {
		configPath = r.configPath()
	}
	mainRaw, mainErr := r.readFile(ctx, configPath)
	if mainErr != nil {
		return shares.WritePlan{}, fmt.Errorf("samba: %s could not be read: %w",
			configPath, mainErr)
	}

	inMain := hasStanza(mainRaw, req.Name)
	destination := DropInFor(req.Name)
	if inMain {
		destination = configPath
	}

	before, _ := r.readFile(ctx, destination)
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

	commands, pathNote, err := r.pathCommands(model, req)
	if err != nil {
		return shares.WritePlan{}, err
	}
	if !inMain {
		if _, statErr := os.Stat(DropInDir); statErr != nil {
			commands = append(commands, BuildMakeDropInDir())
		}
	}
	install, err := BuildInstall(staged, destination)
	if err != nil {
		return shares.WritePlan{}, err
	}
	commands = append(commands, install)

	// A drop-in Samba is not told to read is a file that does nothing, so the
	// one line that reaches it is part of the same change.
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

	if r.smbcontrol != nil {
		commands = append(commands, BuildReload())
	}
	plan.Commands = commands
	plan.Warning = joinNotes(plan.Warning, pathNote)
	return plan, nil
}

// joinNotes puts two caveats in one dialog paragraph, keeping whichever of
// them there is.
func joinNotes(notes ...string) string {
	var kept []string
	for _, note := range notes {
		if strings.TrimSpace(note) != "" {
			kept = append(kept, note)
		}
	}
	return strings.Join(kept, "\n\n")
}

// pathCommands is the part of a share plan that is not a configuration file:
// creating the directory the share exports, and labelling it.
//
// A share whose path does not exist looks to every client like a permission
// problem on the server, and it is the commonest way a new share fails. So the
// directory is offered in the same plan, previewed like everything else — and
// nothing is created when the path is already there, or when the form said no.
func (r *Real) pathCommands(model shares.Model,
	req shares.ShareRequest) ([]shares.Command, string, error) {
	if !Bool(req.CreatePath, false) {
		return nil, "", nil
	}
	path := strings.TrimSpace(req.Path)
	if err := CheckPath(path); err != nil {
		return nil, "", err
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		// The directory is there: its mode and its owner are somebody's
		// decision, and a share form is not the place to overrule them.
		return nil, "", nil
	case !os.IsNotExist(err):
		return nil, "the directory could not be looked at (" +
			runner.FirstLine(err.Error()) + "), so this plan leaves it alone; " +
			"the share is written either way", nil
	}
	if r.install == nil {
		return nil, "the `install` command was not found, so " + path +
			" cannot be created here — the share is written and the directory " +
			"is yours to make", nil
	}

	owner, group := ownerOrRoot(req.Owner), ownerOrRoot(req.Group)
	create, err := BuildMakeShareDir(path, owner, group)
	if err != nil {
		return nil, "", err
	}
	commands := []shares.Command{create}
	note := path + " does not exist yet, so this plan creates it: mode " +
		ShareDirMode + ", owned by " + owner + ":" + group + "."

	if r.enforcing(model) && r.chcon != nil {
		label, labelErr := BuildLabelShareDir(path)
		if labelErr != nil {
			return nil, "", labelErr
		}
		commands = append(commands, label)
		note += " SELinux is enforcing here, so it is also labelled " +
			SELinuxShareType + " — which `chcon` does until the next full " +
			"relabel of the filesystem, not for ever."
	}
	return commands, note, nil
}

// enforcing reports that SELinux is not merely loaded but refusing, which is
// the only state where a missing label stops a client rather than logging one
// line nobody reads.
func (r *Real) enforcing(model shares.Model) bool {
	return model.SELinux.Enabled &&
		strings.EqualFold(strings.TrimSpace(model.SELinux.Mode), "enforcing")
}

// ownerOrRoot is who a created directory belongs to, defaulting to root when
// the form left the field empty.
func ownerOrRoot(name string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	return "root"
}

// BuildShareDelete stages an smb.conf without the share's `include` line and
// returns the plan that removes both it and the file it pointed at.
func (r *Real) BuildShareDelete(ctx context.Context, model shares.Model,
	name string) (shares.WritePlan, error) {
	caps := r.Capabilities()
	if !caps.CanEditShares {
		return shares.WritePlan{}, fmt.Errorf("%s", caps.EditReason)
	}
	if r.rm == nil {
		return shares.WritePlan{}, fmt.Errorf(
			"samba: the `rm` command was not found, so the drop-in file cannot " +
				"be removed here")
	}
	if err := CheckShareName(name); err != nil {
		return shares.WritePlan{}, err
	}

	configPath := model.Global.ConfigFile
	if configPath == "" {
		configPath = r.configPath()
	}
	mainRaw, mainErr := r.readFile(ctx, configPath)
	if mainErr != nil {
		return shares.WritePlan{}, fmt.Errorf("samba: %s could not be read: %w",
			configPath, mainErr)
	}
	dropIn := DropInFor(name)
	dropInRaw, _ := r.readFile(ctx, dropIn)
	if err := checkOwned(name, configPath, mainRaw, dropIn, dropInRaw); err != nil {
		return shares.WritePlan{}, err
	}

	include := IncludeLineFor(name)
	updated, err := RemoveInclude(mainRaw, include)
	if err != nil {
		return shares.WritePlan{}, err
	}
	staged, err := Stage("smb.conf", updated)
	if err != nil {
		return shares.WritePlan{}, err
	}

	plan := shares.WritePlan{
		Title:    "Remove the share [" + name + "]",
		Path:     dropIn,
		TempPath: staged,
		Diff:     shares.Diff(configPath, mainRaw, updated) + shares.Diff(dropIn, dropInRaw, ""),
		Warning:  deleteWarning(name, dropIn, configPath, include),
	}

	// smb.conf loses the line first, so the server is never told to read a
	// file that has just stopped existing.
	mainInstall, err := BuildInstall(staged, configPath)
	if err != nil {
		return shares.WritePlan{}, err
	}
	remove, err := BuildRemoveDropIn(dropIn)
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan.Commands = []shares.Command{mainInstall, remove}
	if r.smbcontrol != nil {
		plan.Commands = append(plan.Commands, BuildReload())
	}
	r.validate(ctx, &plan)
	return plan, nil
}

// deleteWarning is what the confirm dialog says a removal does and, more
// usefully, what it does not.
func deleteWarning(name, dropIn, configPath, include string) string {
	return "This removes " + dropIn + " and the line `" + include + "` from " +
		configPath + ". Nothing else in that file changes.\n\n" +
		"The directory [" + name + "] exported and everything in it are left " +
		"exactly as they are: this stops the share being served, it does not " +
		"delete a single file."
}

// checkOwned refuses a share this tool did not write, in the words that say
// which of the several ways that can be true is the one here.
//
// A share is this tool's to remove only when it lives alone in a drop-in this
// tool created and smb.conf reaches it through one `include` line. Anything
// else — a stanza in the distribution's own file, a file somebody else wrote,
// an include from somewhere this tool does not manage — is somebody's
// configuration, and taking a section out of it behind a keystroke is how a
// file server gets a change nobody can explain afterwards.
func checkOwned(name, configPath, mainRaw, dropIn, dropInRaw string) error {
	if hasStanza(mainRaw, name) {
		return fmt.Errorf(
			"samba: [%s] is written in %s itself, not in a drop-in tui-samba "+
				"owns — that file belongs to whoever wrote it, so remove the "+
				"stanza there by hand", name, configPath)
	}
	if dropInRaw == "" {
		return fmt.Errorf(
			"samba: [%s] does not come from %s, so it is defined somewhere "+
				"tui-samba did not write — another `include`, or a file added to "+
				"the configuration by hand. Only a share this tool created is "+
				"removed here", name, dropIn)
	}
	if !strings.HasPrefix(dropInRaw, Header) {
		return fmt.Errorf(
			"samba: %s was not written by tui-samba — it carries no `%s` banner, "+
				"so it is somebody else's file with the same name", dropIn,
			strings.TrimSuffix(firstLine(Header), "\n"))
	}
	if !hasStanza(dropInRaw, name) {
		return fmt.Errorf(
			"samba: %s does not define [%s], so the share is coming from "+
				"somewhere else and removing this file would not remove it",
			dropIn, name)
	}
	includes := ParseIncludes(mainRaw)
	for _, current := range includes {
		if current == dropIn {
			return nil
		}
	}
	return fmt.Errorf(
		"samba: %s exists but %s does not include it, so [%s] is reached some "+
			"other way — removing the file would leave the share where it is",
		dropIn, configPath, name)
}

// BuildGlobalWrite stages the server-wide settings into this tool's own
// drop-in, has Samba's parser read it back, and returns the plan that installs
// it.
func (r *Real) BuildGlobalWrite(ctx context.Context, model shares.Model,
	req shares.GlobalRequest) (shares.WritePlan, error) {
	caps := r.Capabilities()
	if !caps.CanEditShares {
		return shares.WritePlan{}, fmt.Errorf("%s", caps.EditReason)
	}
	configPath := model.Global.ConfigFile
	if configPath == "" {
		configPath = r.configPath()
	}
	mainRaw, mainErr := r.readFile(ctx, configPath)
	if mainErr != nil {
		return shares.WritePlan{}, fmt.Errorf("samba: %s could not be read: %w",
			configPath, mainErr)
	}
	before, _ := r.readFile(ctx, GlobalDropIn)

	plan, err := planGlobalWrite(req, configPath, mainRaw, before)
	if err != nil {
		return shares.WritePlan{}, err
	}
	if !hasDropInDir() {
		plan.Commands = append([]shares.Command{BuildMakeDropInDir()},
			plan.Commands...)
	}
	if r.smbcontrol != nil {
		plan.Commands = append(plan.Commands, BuildReload())
	}
	r.validate(ctx, &plan)
	return plan, nil
}

// hasDropInDir reports whether the drop-in directory is already there, so a
// plan only carries the command that creates it when it is not.
func hasDropInDir() bool {
	_, err := os.Stat(DropInDir)
	return err == nil
}

// planGlobalWrite is the whole server-wide change as a function of the files
// it reads, so the real backend and the sample machine produce the same plan
// from the same bytes.
func planGlobalWrite(req shares.GlobalRequest, configPath, mainRaw,
	before string) (shares.WritePlan, error) {
	stanza, err := RenderGlobal(req, KeepGlobalLines(rawStanza(before, "global")))
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan := shares.WritePlan{
		Title:   "Write the server settings to " + GlobalDropIn,
		Path:    GlobalDropIn,
		Content: RenderDropIn(stanza),
	}
	plan.Diff = shares.Diff(GlobalDropIn, before, plan.Content)

	staged, err := Stage(filepath.Base(GlobalDropIn), plan.Content)
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan.TempPath = staged

	install, err := BuildInstall(staged, GlobalDropIn)
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan.Commands = []shares.Command{install}

	// The include goes at the end of [global], which is exactly what makes
	// this work: an `include` is processed where it appears, so these three
	// parameters are read after the ones the main file sets and are the ones
	// that win.
	include := "include = " + GlobalDropIn
	updated, err := AddInclude(mainRaw, include)
	if err != nil {
		return shares.WritePlan{}, err
	}
	plan.Warning = "These settings are written to a file of tui-samba's own " +
		"rather than into " + configPath + ", and they are read at the end of " +
		"[global], so they are the ones that win over anything set above."
	if updated != mainRaw {
		mainStaged, stageErr := Stage("smb.conf", updated)
		if stageErr != nil {
			return shares.WritePlan{}, stageErr
		}
		mainInstall, installErr := BuildInstall(mainStaged, configPath)
		if installErr != nil {
			return shares.WritePlan{}, installErr
		}
		plan.Commands = append(plan.Commands, mainInstall)
		plan.Diff += shares.Diff(configPath, mainRaw, updated)
		plan.Warning += " One line is added to " + configPath + ": `" +
			include + "`. Nothing else in that file changes."
	}
	return plan, nil
}

// validate runs Samba's own parser over the staged file and records what it
// said.
//
// It runs before the confirm dialog opens, because a configuration smbd would
// refuse is not something to find out after the file is in /etc. What it
// checks is the staged file itself: for a drop-in that is its syntax and its
// parameter names, with the globals testparm fills in from its own defaults.
func (r *Real) validate(ctx context.Context, plan *shares.WritePlan) {
	if r.testparm == nil || plan.TempPath == "" {
		plan.Validation = "could not run: testparm is not installed"
		return
	}
	cmd, err := BuildValidate(plan.TempPath)
	if err != nil {
		plan.Validation = "could not run: " + err.Error()
		return
	}
	plan.ValidationCommand = r.testparm.Preview(cmd)
	out, err := r.testparm.Read(ctx, cmd.Argv...)
	if err != nil {
		plan.Validation = "was refused by Samba's own parser: " +
			runner.FirstLine(strings.TrimSpace(out))
		return
	}
	plan.Validated = true
	plan.Validation = "Samba's own parser read the staged file: " +
		summariseTestparm(out)
}

// summariseTestparm keeps the one line of testparm output worth showing in a
// dialog.
func summariseTestparm(out string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Loaded services file") {
			return trimmed
		}
	}
	return "no error"
}

// hasStanza reports whether a raw configuration file defines a section.
func hasStanza(raw, name string) bool {
	start, _ := findStanza(splitLines(raw), name)
	return start >= 0
}

// rawStanza returns one section's lines as they are written in a file.
func rawStanza(raw, name string) []string {
	lines := splitLines(raw)
	start, end := findStanza(lines, name)
	if start < 0 {
		return nil
	}
	return lines[start:end]
}

// BuildUserAdd adds an account to the Samba password database.
func (r *Real) BuildUserAdd(model shares.Model, name,
	password string) (shares.Command, error) {
	if err := r.haveUsers(); err != nil {
		return shares.Command{}, err
	}
	if _, exists := model.User(name); exists {
		return shares.Command{}, fmt.Errorf(
			"samba: %s is already in the Samba password database — press p to "+
				"set its password instead", name)
	}
	if err := CheckUser(name); err != nil {
		return shares.Command{}, err
	}
	if !r.lookup(name) {
		return shares.Command{}, fmt.Errorf(
			"samba: there is no Unix account called %s, and Samba maps every "+
				"entry to one; create the Unix account first — tui-users is the "+
				"tool for that", name)
	}
	return BuildUserAdd(name, password)
}

// BuildUserAction is delete, enable and disable.
func (r *Real) BuildUserAction(_ shares.Model, name,
	action string) (shares.Command, error) {
	if err := r.haveUsers(); err != nil {
		return shares.Command{}, err
	}
	return BuildUserAction(name, action)
}

// BuildSetPassword replaces one account's Samba password.
func (r *Real) BuildSetPassword(_ shares.Model, name,
	password string) (shares.Command, error) {
	if err := r.haveUsers(); err != nil {
		return shares.Command{}, err
	}
	return BuildSetPassword(name, password)
}

// haveUsers refuses an account action on a machine with no smbpasswd, in the
// words the status line shows.
func (r *Real) haveUsers() error {
	if r.smbpasswd == nil {
		return fmt.Errorf("smbpasswd is not installed on this machine")
	}
	return nil
}

// BuildReload tells the running server to re-read its configuration.
func (r *Real) BuildReload(_ shares.Model) (shares.Command, error) {
	if r.smbcontrol == nil {
		return shares.Command{}, fmt.Errorf(
			"smbcontrol is not installed, so the running server cannot be told " +
				"to re-read anything")
	}
	return BuildReload(), nil
}

// BuildSelfTest asks the server for its own share list.
func (r *Real) BuildSelfTest(_ shares.Model) (shares.Command, error) {
	if r.smbclient == nil {
		return shares.Command{}, fmt.Errorf(
			"smbclient is not installed, so this server cannot be asked what a " +
				"client sees")
	}
	return BuildSelfTest("localhost")
}
