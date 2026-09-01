// Package shares defines the backend-agnostic model tui-samba renders and the
// interface every file-sharing backend satisfies. The UI knows only these
// types: it never builds a testparm, pdbedit or smbpasswd argv itself, and it
// never opens a file. Mutations are Command values produced by the backend,
// shown in a preview dialog and only then executed.
package shares

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tui-tools/tui-kit/runner"
)

// Command is a single invocation the user is about to run. Argv excludes any
// privilege wrapper: the backend adds it when previewing and when executing.
//
// It is an alias rather than a type of its own, so a backend hands the very
// value the confirm dialog displayed straight to the kit runner, with no
// conversion in between. That identity is what makes the preview a promise.
type Command = runner.Command

// Verdict is what tui-samba thinks of one share. It is a string rather than an
// enum so `--check` reports a word a script can grep for.
type Verdict string

// The four verdicts. VerdictNone is the zero value and means "nothing to say
// about this one", which is what an ordinary read-only share deserves.
const (
	VerdictNone Verdict = ""
	// VerdictOK is a share that is exported the way its own settings say.
	VerdictOK Verdict = "ok"
	// VerdictWarn is something worth seeing before somebody finds it.
	VerdictWarn Verdict = "warn"
	// VerdictRisk is a share that is handing out more than anybody meant.
	VerdictRisk Verdict = "risk"
)

// rank orders the verdicts worst first, which is the order the share list is
// sorted in.
func rank(v Verdict) int {
	switch v {
	case VerdictRisk:
		return 0
	case VerdictWarn:
		return 1
	case VerdictOK:
		return 2
	default:
		return 3
	}
}

// The kinds a Finding can carry. They are the sentences the share list is
// sorted by, and the keys `--check` counts.
const (
	// FindingWorldWritable is a share whose directory is mode o+w on the Unix
	// side, which no Samba setting can take back.
	FindingWorldWritable = "world-writable"
	// FindingGuestWritable is a writable share that accepts a connection with
	// no password.
	FindingGuestWritable = "guest-writable"
	// FindingPathMissing is a share whose path does not exist, which every
	// client sees as an access denied it cannot explain.
	FindingPathMissing = "path-missing"
	// FindingMapToGuest is `map to guest = Bad User` on a server that has guest
	// shares: an unknown account silently becomes the guest instead of being
	// refused.
	FindingMapToGuest = "map-to-guest"
	// FindingSMB1 is a server still answering the protocol WannaCry travelled
	// on.
	FindingSMB1 = "smb1"
	// FindingSecurityShare is the `security = share` mode, removed in Samba
	// 4.0 and still copied into configurations from tutorials.
	FindingSecurityShare = "security-share"
	// FindingNoAccessControl is a writable share with neither `valid users`
	// nor a `write list`: every account on the server can write to it.
	FindingNoAccessControl = "no-access-control"
	// FindingUnreadable is a configuration this user could not read.
	FindingUnreadable = "unreadable"
)

// Finding is one thing worth saying about a share or about the server, in the
// user's terms.
type Finding struct {
	// Kind is one of the constants above, so a script can group on it.
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	// Message is the one sentence the screen shows.
	Message string `json:"message"`
}

// DirInfo is what the Unix side says about a share's directory.
//
// It is here because half of what people get wrong about a Samba share is not
// in smb.conf at all. A share marked read only on a directory that is mode
// 0777 is one `read only = no` away from being writable by everybody, and a
// share whose path does not exist looks to every client like a permission
// problem on the server.
type DirInfo struct {
	// Path is the directory the share exports, empty for a share that has
	// none ([homes] and the printer shares).
	Path string `json:"path,omitempty"`
	// Exists reports that something is there.
	Exists bool `json:"exists"`
	// Mode is the permission bits in octal ("0755"), empty when unknown.
	Mode string `json:"mode,omitempty"`
	// Owner and Group are the names `stat` printed.
	Owner string `json:"owner,omitempty"`
	Group string `json:"group,omitempty"`
	// WorldWritable is the o+w bit, which is the finding.
	WorldWritable bool `json:"worldWritable"`
	// IsDir reports that the path is a directory rather than a file.
	IsDir bool `json:"isDir"`
	// SELinuxContext is the label `ls -Z` printed, empty when SELinux is not
	// enabled or the path could not be read.
	SELinuxContext string `json:"selinuxContext,omitempty"`
	// Note explains an unknown answer: the path could not be stat'ed, and why.
	Note string `json:"note,omitempty"`
}

// Share is one stanza of the effective configuration.
type Share struct {
	// Name is the stanza name without its brackets.
	Name string `json:"name"`
	// Path, Comment and the flags are the parameters worth a column. Every
	// one of them is also in Params; these are the ones the UI reads by name.
	Path    string `json:"path,omitempty"`
	Comment string `json:"comment,omitempty"`
	// Browseable reports whether the share appears in a listing. A share that
	// is not browseable is still reachable by name, which is why it is shown.
	Browseable bool `json:"browseable"`
	ReadOnly   bool `json:"readOnly"`
	GuestOK    bool `json:"guestOk"`
	// ValidUsers and WriteList are the access lists, already split.
	ValidUsers []string `json:"validUsers,omitempty"`
	WriteList  []string `json:"writeList,omitempty"`
	// CreateMask and DirectoryMask are the modes a new file and a new
	// directory get, as octal strings.
	CreateMask    string `json:"createMask,omitempty"`
	DirectoryMask string `json:"directoryMask,omitempty"`
	// InheritPermissions reports that new files take their mode from the
	// parent directory instead of from the masks.
	InheritPermissions bool `json:"inheritPermissions"`
	// VFSObjects are the modules stacked on the share.
	VFSObjects []string `json:"vfsObjects,omitempty"`
	// Special marks a stanza that is not an ordinary directory export:
	// [homes], [printers] and [print$].
	Special bool `json:"special"`
	// Params is every effective parameter of the stanza, as the server itself
	// resolved it. The detail screen shows it, so a setting this tool has no
	// field for is still visible.
	Params map[string]string `json:"params,omitempty"`
	// Raw is the stanza as the effective dump printed it, which is what the
	// detail screen shows verbatim.
	Raw []string `json:"raw,omitempty"`
	// Dir is the Unix side of the path.
	Dir DirInfo `json:"dir"`
	// Verdict and Findings are what tui-samba thinks of the share.
	Verdict  Verdict   `json:"verdict"`
	Findings []Finding `json:"findings,omitempty"`
}

// Writable reports that a client that gets in can write, which is `read only =
// no` however it was spelled.
func (s Share) Writable() bool { return !s.ReadOnly }

// Has reports whether the share carries a finding of one kind.
func (s Share) Has(kind string) bool {
	for _, finding := range s.Findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}

// Access renders the access lists the way the detail screen reads them.
func (s Share) Access() string {
	switch {
	case s.GuestOK && len(s.ValidUsers) == 0:
		return "anyone, no password"
	case len(s.ValidUsers) > 0:
		return strings.Join(s.ValidUsers, ", ")
	default:
		return "any account on this server"
	}
}

// Haystack is the text the filter matches a share against.
func (s Share) Haystack() string {
	parts := []string{s.Name, s.Path, s.Comment, string(s.Verdict),
		s.Dir.Owner, s.Dir.Group, s.Dir.Mode}
	parts = append(parts, s.ValidUsers...)
	parts = append(parts, s.WriteList...)
	parts = append(parts, s.VFSObjects...)
	for _, finding := range s.Findings {
		parts = append(parts, finding.Kind, finding.Message)
	}
	return strings.Join(parts, " ")
}

// ParamKeys is the stanza's effective parameters, in a stable order. The
// detail screen shows them all, so a setting this tool has no field for is
// still visible — and the order must not reshuffle between two reads of the
// same machine.
func (s Share) ParamKeys() []string { return sortedKeys(s.Params) }

// sortedKeys returns a parameter map's keys in alphabetical order.
func sortedKeys(params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Global is the server-wide half of the effective configuration.
type Global struct {
	// ConfigFile is the file the server reads, as the server itself named it.
	ConfigFile string `json:"configFile,omitempty"`
	// Workgroup and ServerString are what a client sees in a browse list.
	Workgroup    string `json:"workgroup,omitempty"`
	ServerString string `json:"serverString,omitempty"`
	// Security is the authentication mode: "USER" on every supported server.
	Security string `json:"security,omitempty"`
	// MapToGuest is what happens to a login the server does not know.
	MapToGuest string `json:"mapToGuest,omitempty"`
	// MinProtocol and MaxProtocol are the dialect window, as the server
	// resolved it.
	MinProtocol string `json:"minProtocol,omitempty"`
	MaxProtocol string `json:"maxProtocol,omitempty"`
	// SMB1Enabled reports that the minimum dialect is below SMB2.
	SMB1Enabled bool `json:"smb1Enabled"`
	// Includes are the `include = ` lines the main configuration carries,
	// which is what decides whether a drop-in can be used at all.
	Includes []string `json:"includes,omitempty"`
	// Params is every effective global parameter.
	Params map[string]string `json:"params,omitempty"`
	// Findings are the server-wide ones: SMB1, `security = share`, a guest
	// mapping with guest shares behind it.
	Findings []Finding `json:"findings,omitempty"`
}

// ParamKeys is the effective global parameters, in a stable order.
func (g Global) ParamKeys() []string { return sortedKeys(g.Params) }

// User is one account in the Samba password database.
//
// It is not a Unix account. Samba keeps its own database and every entry in it
// has to map to a Unix account that already exists — an entry whose Unix side
// is gone is an account nobody can log in as, and that is worth saying.
type User struct {
	Name string `json:"name"`
	// UID is the Unix user id the entry maps to, -1 when it could not be read.
	UID int `json:"uid"`
	// Flags are the account flags, as pdbedit printed them ("U", "D", "N").
	Flags string `json:"flags,omitempty"`
	// Disabled and NoPassword are the two flags worth a column.
	Disabled   bool `json:"disabled"`
	NoPassword bool `json:"noPassword"`
	// PasswordLastSet is when the password was last changed, verbatim.
	PasswordLastSet string `json:"passwordLastSet,omitempty"`
	// HomeDirectory and Profile are what the entry carries for a Windows
	// client, usually empty on a plain file server.
	HomeDirectory string `json:"homeDirectory,omitempty"`
	// UnixPresent reports that a Unix account of this name exists.
	UnixPresent bool `json:"unixPresent"`
	// Note explains an entry worth a second look, in one sentence.
	Note string `json:"note,omitempty"`
}

// Haystack is the text the filter matches a user against.
func (u User) Haystack() string {
	return strings.Join([]string{u.Name, u.Flags, u.PasswordLastSet, u.Note,
		strconv.Itoa(u.UID)}, " ")
}

// Session is one authenticated client connection.
type Session struct {
	// PID is the smbd process serving it, which is what a per-session command
	// is addressed to.
	PID string `json:"pid"`
	// User and Group are the Unix identity the session was mapped to.
	User  string `json:"user,omitempty"`
	Group string `json:"group,omitempty"`
	// Machine is the client's name and Remote its address.
	Machine string `json:"machine,omitempty"`
	Remote  string `json:"remote,omitempty"`
	// Protocol is the negotiated dialect ("SMB3_11"), Encryption and Signing
	// what is protecting it.
	Protocol   string `json:"protocol,omitempty"`
	Encryption string `json:"encryption,omitempty"`
	Signing    string `json:"signing,omitempty"`
	// SessionID is smbstatus's own identifier for the session.
	SessionID string `json:"sessionId,omitempty"`
}

// Haystack is the text the filter matches a session against.
func (s Session) Haystack() string {
	return strings.Join([]string{s.PID, s.User, s.Group, s.Machine, s.Remote,
		s.Protocol, s.Encryption, s.Signing}, " ")
}

// TreeConnect is one share a session has open.
type TreeConnect struct {
	Service string `json:"service"`
	PID     string `json:"pid"`
	Machine string `json:"machine,omitempty"`
	// Since is when the connection was made, verbatim.
	Since string `json:"since,omitempty"`
	// Encryption and Signing are per-connection, and differ from the
	// session's when a share sets `smb encrypt`.
	Encryption string `json:"encryption,omitempty"`
	Signing    string `json:"signing,omitempty"`
}

// OpenFile is one file a client is holding open.
type OpenFile struct {
	PID  string `json:"pid"`
	User string `json:"user,omitempty"`
	// Path is the file as smbstatus reported it.
	Path string `json:"path"`
	// Service is the share it is under, empty when smbstatus did not say.
	Service string `json:"service,omitempty"`
	// Access and Oplock are the sharing mode and the cached lock.
	Access string `json:"access,omitempty"`
	Oplock string `json:"oplock,omitempty"`
	// Locked reports that a byte-range lock is held on it, which is what a
	// client sees as "the file is in use".
	Locked bool `json:"locked"`
}

// Service is one systemd unit the file server runs as.
//
// The units are not named the same everywhere: Fedora and Arch ship
// `smb.service` and `nmb.service`, Debian and Ubuntu ship `smbd.service` and
// `nmbd.service`. The model carries whichever this machine has, by the name it
// really has, so nothing on screen is a guess.
type Service struct {
	// Unit is the unit name as it exists here.
	Unit string `json:"unit"`
	// Role is what it does, in one word: "file server", "netbios", "winbind".
	Role string `json:"role"`
	// Present reports that the unit is known to systemd at all.
	Present bool `json:"present"`
	Enabled bool `json:"enabled"`
	Active  bool `json:"active"`
	// State is the ActiveState word systemd printed, and Enablement the
	// UnitFileState.
	State      string `json:"state,omitempty"`
	Enablement string `json:"enablement,omitempty"`
	// Detail is why there is nothing, when there is nothing.
	Detail string `json:"detail,omitempty"`
}

// Port is one listening socket the server has open.
type Port struct {
	Port int `json:"port"`
	// Address is the local address it is bound to.
	Address string `json:"address,omitempty"`
	// Process is the program holding it, when the reader was allowed to see.
	Process string `json:"process,omitempty"`
}

// SELinux is the policy state, and the two booleans that decide whether Samba
// may export an ordinary directory at all.
//
// It is here because on Fedora and RHEL a share can be perfect in smb.conf,
// perfect in its Unix modes, and still refuse every client — the directory
// carries the wrong label, and nothing in Samba's own output says so.
type SELinux struct {
	// Enabled reports that a policy is being enforced or at least loaded.
	Enabled bool `json:"enabled"`
	// Mode is the word `getenforce` printed.
	Mode string `json:"mode,omitempty"`
	// Booleans are the samba_export_* switches, by name.
	Booleans map[string]bool `json:"booleans,omitempty"`
	// Detail explains an unread state.
	Detail string `json:"detail,omitempty"`
}

// Counts is the summary `--check` prints and the header shows.
type Counts struct {
	Shares      int `json:"shares"`
	Writable    int `json:"writable"`
	Guest       int `json:"guest"`
	Users       int `json:"users"`
	Disabled    int `json:"disabledUsers"`
	Sessions    int `json:"sessions"`
	OpenFiles   int `json:"openFiles"`
	Findings    int `json:"findings"`
	Risks       int `json:"risks"`
	PathMissing int `json:"pathMissing"`
}

// Model is the whole picture tui-samba renders.
type Model struct {
	// Backend names the implementation that produced this model.
	Backend string `json:"backend"`
	// Installed reports that this machine has a Samba server at all. A machine
	// without one is a normal machine, not a failure, and Detail says so.
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"`
	// Version is what the server itself printed, empty when it could not be
	// asked.
	Version string `json:"version,omitempty"`

	Global Global  `json:"global"`
	Shares []Share `json:"shares"`
	// ConfigDetail explains an empty share list: the configuration could not
	// be read, and by whom.
	ConfigDetail string `json:"configDetail,omitempty"`

	Users []User `json:"users"`
	// UsersDetail explains an empty user list, which on an unprivileged run is
	// the ordinary case: the password database is root-only.
	UsersDetail string `json:"usersDetail,omitempty"`

	Sessions     []Session     `json:"sessions"`
	TreeConnects []TreeConnect `json:"treeConnects,omitempty"`
	OpenFiles    []OpenFile    `json:"openFiles,omitempty"`
	// StatusDetail explains an empty session list.
	StatusDetail string `json:"statusDetail,omitempty"`

	Services []Service `json:"services,omitempty"`
	Ports    []Port    `json:"ports,omitempty"`
	// PortsDetail explains why no port could be read.
	PortsDetail string `json:"portsDetail,omitempty"`

	SELinux SELinux `json:"selinux"`
	// Hostname is this machine's name, which is what a client types.
	Hostname string `json:"hostname,omitempty"`
	// Now is the instant the read was made.
	Now time.Time `json:"now"`
}

// Share returns one share by name.
func (m Model) Share(name string) (Share, bool) {
	for _, share := range m.Shares {
		if share.Name == name {
			return share, true
		}
	}
	return Share{}, false
}

// User returns one account by name.
func (m Model) User(name string) (User, bool) {
	for _, user := range m.Users {
		if user.Name == name {
			return user, true
		}
	}
	return User{}, false
}

// SessionsOn is how many sessions have a share open right now, which is the
// "who uses it" column and the reason a change is worth thinking about twice.
func (m Model) SessionsOn(share string) int {
	count := 0
	for _, connection := range m.TreeConnects {
		if connection.Service == share {
			count++
		}
	}
	return count
}

// FilesOn is how many files are open under a share.
func (m Model) FilesOn(share string) int {
	count := 0
	for _, file := range m.OpenFiles {
		if file.Service == share {
			count++
		}
	}
	return count
}

// Count summarises the model.
//
// The special stanzas are counted as shares, because they are shares: [homes]
// exports every account's home directory and is the single most consequential
// line in a default smb.conf. What they are not is a directory this tool will
// stat, and that is a different question.
func (m Model) Count() Counts {
	var c Counts
	for _, share := range m.Shares {
		c.Shares++
		if share.Writable() {
			c.Writable++
		}
		if share.GuestOK {
			c.Guest++
		}
		if share.Has(FindingPathMissing) {
			c.PathMissing++
		}
		switch share.Verdict {
		case VerdictRisk:
			c.Risks++
			c.Findings++
		case VerdictWarn:
			c.Findings++
		}
	}
	for _, finding := range m.Global.Findings {
		switch finding.Verdict {
		case VerdictRisk:
			c.Risks++
			c.Findings++
		case VerdictWarn:
			c.Findings++
		}
	}
	c.Users = len(m.Users)
	for _, user := range m.Users {
		if user.Disabled {
			c.Disabled++
		}
	}
	c.Sessions = len(m.Sessions)
	c.OpenFiles = len(m.OpenFiles)
	return c
}

// Serving reports that something on this machine is actually answering, which
// is the one question the service screen exists to settle.
func (m Model) Serving() bool {
	for _, service := range m.Services {
		if service.Role == RoleFileServer && service.Active {
			return true
		}
	}
	return false
}

// The roles a unit plays, named rather than numbered so `--check` reports a
// word.
const (
	RoleFileServer = "file server"
	RoleNetBIOS    = "netbios"
	RoleWinbind    = "winbind"
)

// SortShares orders the list findings-first: what is handing out too much,
// then what is about to confuse somebody, then the rest by name. A reader
// arrives with "what is wrong here", and the answer to that must not be
// somewhere in an alphabetical list.
func SortShares(list []Share) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if ra, rb := rank(a.Verdict), rank(b.Verdict); ra != rb {
			return ra < rb
		}
		// An ordinary export before a special stanza: [homes] and [printers]
		// are always there and are rarely what somebody came to look at.
		if a.Special != b.Special {
			return !a.Special
		}
		return a.Name < b.Name
	})
}

// Capabilities tells the UI what a backend supports, so the key map is built
// from the backend rather than hardcoded.
type Capabilities struct {
	// CanEditShares reports that a share can be written, and EditReason
	// explains a false one in the user's terms.
	CanEditShares bool
	EditReason    string
	// ConfigPath is the server's own configuration file, and DropInDir the
	// directory this tool writes a new share into. Which of the two a
	// particular change lands in is a per-share question the backend answers
	// in the WritePlan, because it depends on where the share is defined now.
	ConfigPath string
	DropInDir  string
	// CanManageUsers reports that the password database can be changed, and
	// UsersReason explains a false one.
	CanManageUsers bool
	UsersReason    string
	// CanReload reports that the running server can be told to re-read its
	// configuration.
	CanReload bool
	// CanSelfTest reports that the server can be asked to list its own shares.
	CanSelfTest bool
	// StatusJSON reports that smbstatus answered in JSON, which is what
	// decides whether the connections screen carries the encryption and
	// signing columns.
	StatusJSON bool
}

// ShareRequest is what the share form collected. Every value is still a
// string, because what a path and an access list may be is the backend's rule,
// checked once, where the argv and the file are built.
type ShareRequest struct {
	// Original is the share being edited, empty for a new one.
	Original string
	Name     string
	Path     string
	Comment  string
	// Browseable, ReadOnly and GuestOK are collected as "yes"/"no", which is
	// how they are written into the file.
	Browseable string
	ReadOnly   string
	GuestOK    string
	// ValidUsers and WriteList are as typed, space or comma separated.
	ValidUsers string
	WriteList  string
	// CreateMask and DirectoryMask are octal strings.
	CreateMask    string
	DirectoryMask string
	// CreatePath is "yes" when the plan may also create the exported
	// directory. It is collected as a string like the other flags, and it only
	// produces a command when the directory really is not there: a share
	// whose path exists is never touched on the Unix side.
	CreatePath string
	// Owner and Group are who a directory this plan creates belongs to. They
	// are ignored when the directory already exists.
	Owner string
	Group string
}

// GlobalRequest is what the server-wide form collected. Like a ShareRequest
// every value is still a string, because what a workgroup, a dialect and a
// host list may be is the backend's rule, checked once, where the file is
// built.
type GlobalRequest struct {
	// Workgroup is the NetBIOS workgroup a client sees in a browse list.
	Workgroup string
	// MinProtocol is the lowest dialect the server will speak, as Samba spells
	// it ("SMB2_02").
	MinProtocol string
	// HostsAllow is as typed, space or comma separated.
	HostsAllow string
}

// WritePlan is a change the user is about to make: what the file will look
// like, how that differs from what is there now, whether the server's own
// parser accepted it, and the exact commands that apply it.
type WritePlan struct {
	// Title names the change for the dialog and the status line.
	Title string
	// Path is the destination file.
	Path string
	// Content is the text that will be installed.
	Content string
	// Diff is the unified diff against the current file, empty when nothing
	// would change.
	Diff string
	// TempPath is the staging file the install command copies from.
	TempPath string
	// Validation is what the server's own parser said about the staged
	// content, and ValidationCommand is the command line that asked. The check
	// runs before the user is asked to confirm, because a share smbd will not
	// accept is not something to discover after the file is in /etc.
	Validation        string
	ValidationCommand string
	// Validated reports that the check ran and passed. False with an empty
	// Validation means the check could not run at all.
	Validated bool
	// Warning is a caveat the confirm dialog must show.
	Warning string
	// Commands are run in order, and are what the confirm dialog shows.
	Commands []Command
}

// The user actions a backend understands. They are named rather than spelled
// as flags so the UI never carries a `smbpasswd` argument.
const (
	UserDelete  = "delete"
	UserEnable  = "enable"
	UserDisable = "disable"
)

// Backend is the boundary between the UI and the machine. Load reads state;
// the Build* methods turn user intent into previewable Commands; Run executes
// a Command the user confirmed. Nothing else may mutate the system.
type Backend interface {
	// Name is the backend identifier ("samba").
	Name() string
	// Describe is the one-line summary shown in the header.
	Describe() string
	// Capabilities reports what this backend supports.
	Capabilities() Capabilities

	// Preview renders the exact command line Run will execute, privilege
	// wrapper included. This is the text shown in the confirm dialog.
	Preview(cmd Command) string

	// Load reads the file server's state.
	Load(ctx context.Context) (Model, error)
	// Run executes a previously previewed command.
	Run(ctx context.Context, cmd Command) (string, error)

	// BuildShareWrite stages the new configuration, has the server's own
	// parser check it, and returns the plan that installs it.
	BuildShareWrite(ctx context.Context, model Model, req ShareRequest) (WritePlan, error)
	// BuildShareDelete returns the plan that removes a share this tool wrote:
	// its drop-in file, and the one `include` line that reached it. A share
	// defined anywhere else is refused with the reason, because removing it
	// would mean editing a file somebody else owns.
	BuildShareDelete(ctx context.Context, model Model, name string) (WritePlan, error)
	// BuildGlobalWrite stages the server-wide settings into a drop-in of this
	// tool's own, checked and diffed like a share.
	BuildGlobalWrite(ctx context.Context, model Model, req GlobalRequest) (WritePlan, error)

	// BuildUserAdd adds an account to the password database. The password is
	// carried on the command's standard input, never in an argv.
	BuildUserAdd(model Model, name, password string) (Command, error)
	// BuildUserAction is delete, enable and disable: one verb, one account.
	BuildUserAction(model Model, name, action string) (Command, error)
	// BuildSetPassword replaces one account's password, again through stdin.
	BuildSetPassword(model Model, name, password string) (Command, error)

	// BuildReload tells the running server to re-read its configuration.
	BuildReload(model Model) (Command, error)
	// BuildSelfTest asks the server to list its own shares, the way a client
	// would. It is a read, and it is previewed like everything else because it
	// is a command the user is choosing to run.
	BuildSelfTest(model Model) (Command, error)
}
