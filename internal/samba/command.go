package samba

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/tui-tools/tui-samba/internal/shares"
)

// This file builds every argv the tool can produce, and renders every line it
// can write into a configuration file. They are functions of their arguments
// and nothing else — no clock, no filesystem, no process — so a test can
// assert on the exact command line the confirm dialog will show, and the
// dialog and the execution consume the same value.

// The programs this backend drives. `smbd` is only ever asked its version: the
// server itself is never started, stopped or reconfigured by this tool.
const (
	// BinSmbd is the server. It lives in an sbin directory on every
	// distribution, which is why the manifest declares search paths for it.
	BinSmbd = "smbd"
	// BinTestparm dumps the effective configuration and checks a staged one.
	BinTestparm = "testparm"
	// BinPdbedit lists the Samba password database.
	BinPdbedit = "pdbedit"
	// BinSmbpasswd is the only thing that changes it.
	BinSmbpasswd = "smbpasswd"
	// BinSmbstatus reports sessions, connections and open files.
	BinSmbstatus = "smbstatus"
	// BinSmbcontrol is how a running server is told to re-read its
	// configuration.
	BinSmbcontrol = "smbcontrol"
	// BinSmbclient is the self-test: ask the server for its own share list the
	// way a client would.
	BinSmbclient = "smbclient"
)

// The version-gated capabilities of the backend, named the way the manifest
// names them. The tool asks the compat set for these instead of comparing
// version numbers in code.
const (
	// FeatureStatusJSON is `smbstatus --json`, which arrived in Samba 4.17.
	// Without it the same facts are read out of the text output, which carries
	// the sessions, the connections and the open files but not the per-session
	// encryption and signing state.
	FeatureStatusJSON = "status-json"
)

// ConfigPath is the file every supported distribution's Samba reads. It is a
// default: the server names its own configuration file in the first line of
// `testparm` output, and that is what the model carries.
const ConfigPath = "/etc/samba/smb.conf"

// DropInDir is where tui-samba writes the shares it creates.
//
// A directory of its own, rather than appending to smb.conf, so a share this
// tool wrote is a file this tool owns and a reader can delete it without
// picking apart somebody else's configuration. Samba's `include` is a literal
// textual inclusion processed where it appears, so one line per file is how a
// drop-in is reached — there is no include-a-directory form, and inventing one
// would be inventing a Samba feature.
const DropInDir = "/etc/samba/tui-samba.d"

// The modes a written file and the drop-in directory get. Neither carries a
// secret: smb.conf is world-readable on every distribution, and a mode that
// hid it from the server would be a mode the server could not read.
const (
	FileMode = "644"
	DirMode  = "755"
)

// The stanzas this tool will not write. [global] is not a share; [homes],
// [printers] and [print$] are shares whose meaning comes from Samba itself,
// and a form with a path field has nothing useful to say about any of them.
var specialStanzas = map[string]bool{
	"global": true, "homes": true, "printers": true, "print$": true,
}

// IsSpecial reports whether a stanza is one of Samba's own rather than an
// ordinary directory export.
func IsSpecial(name string) bool { return specialStanzas[strings.ToLower(name)] }

// shareNameRe is what a share may be called. It is narrower than Samba's own
// rule on purpose: the name becomes a section header in a file and a file name
// in the drop-in directory, so anything that could close the bracket early or
// walk out of the directory is refused here rather than after the write.
var shareNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,62}$`)

// pathRe is what a share may export. Absolute, and made of the characters a
// path in a configuration file can carry without quoting.
var pathRe = regexp.MustCompile(`^/[A-Za-z0-9._/ -]{0,200}$`)

// userRe is an account name. The `$` suffix is a machine account, which is a
// real thing in a Samba database; the optional `@` or `+` prefix is a group,
// which is what `valid users` accepts alongside accounts.
var userRe = regexp.MustCompile(`^[@+]?[a-z_][a-z0-9_.-]{0,31}\$?$`)

// maskRe is an octal file mode, with or without its leading zero.
var maskRe = regexp.MustCompile(`^0?[0-7]{3,4}$`)

// stagedRe accepts the staging path an install command copies from. It is a
// path this package built, and it is checked anyway.
var stagedRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// destinationRe accepts a file this tool will install over: the server's own
// configuration, or a file in the drop-in directory.
func validDestination(destination string) bool {
	if strings.Contains(destination, "..") {
		return false
	}
	if destination == ConfigPath {
		return true
	}
	rest, found := strings.CutPrefix(destination, DropInDir+"/")
	return found && strings.HasSuffix(rest, ".conf") &&
		shareNameRe.MatchString(strings.TrimSuffix(rest, ".conf"))
}

// DropInFor is the file a share this tool writes lives in.
func DropInFor(name string) string { return path.Join(DropInDir, name+".conf") }

// IncludeLineFor is the one line smb.conf needs for a drop-in to be read.
func IncludeLineFor(name string) string { return "include = " + DropInFor(name) }

// CheckShareName rejects a share name this tool will not write.
func CheckShareName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("samba: a share needs a name")
	}
	if IsSpecial(name) {
		return fmt.Errorf(
			"samba: [%s] is one of Samba's own sections, not a directory "+
				"export — tui-samba shows it and will not rewrite it", name)
	}
	if !shareNameRe.MatchString(name) {
		return fmt.Errorf(
			"samba: %q is not a share name — letters, digits, dot, dash and "+
				"underscore only, and it may not start with one of the "+
				"punctuation characters", name)
	}
	return nil
}

// CheckPath rejects a directory this tool will not put in a configuration.
func CheckPath(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("samba: a share needs a path to export")
	}
	if !strings.HasPrefix(value, "/") {
		return fmt.Errorf("samba: %q is not an absolute path", value)
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("samba: %q walks out of itself with a `..`", value)
	}
	if !pathRe.MatchString(value) {
		return fmt.Errorf(
			"samba: %q is not a path tui-samba will write into a configuration "+
				"file", value)
	}
	return nil
}

// CheckUser rejects an account name this tool will not pass to smbpasswd or
// write into an access list.
func CheckUser(name string) error {
	if !userRe.MatchString(strings.TrimSpace(name)) {
		return fmt.Errorf("samba: %q is not an account name", name)
	}
	return nil
}

// CheckPassword rejects a password this tool will not hand to smbpasswd.
//
// The only rule is about the transport: the password is written to the
// process's standard input as one line, so a value carrying a newline would be
// read as a password and then as an answer to the retype prompt. Everything
// else — length, complexity — is the machine's policy and not this tool's
// opinion.
func CheckPassword(password string) error {
	if password == "" {
		return fmt.Errorf("samba: a password cannot be empty")
	}
	if strings.ContainsAny(password, "\n\r") {
		return fmt.Errorf(
			"samba: a password cannot contain a newline — it is written to " +
				"smbpasswd's standard input as a single line")
	}
	if len(password) > 255 {
		return fmt.Errorf("samba: a password longer than 255 characters is refused")
	}
	return nil
}

// checkValue rejects a configuration value that could smuggle a second
// directive into the file it is written to.
func checkValue(key, value string) error {
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("samba: %s cannot contain a newline", key)
	}
	if strings.ContainsAny(value, "#;[]") {
		return fmt.Errorf(
			"samba: %s cannot contain # ; [ or ] — smb.conf reads them as a "+
				"comment or a new section", key)
	}
	return nil
}

// SplitList splits an access list the way somebody types one: on spaces or
// commas, either of which is what they meant.
func SplitList(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	})
}

// normalizeBool reads the several spellings Samba accepts for a boolean and
// returns the one this tool writes.
func normalizeBool(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "y", "true", "1", "on":
		return "yes", true
	case "no", "n", "false", "0", "off":
		return "no", true
	default:
		return "", false
	}
}

// Bool reads a Samba boolean parameter, defaulting when it is absent or
// unparsable.
func Bool(value string, fallback bool) bool {
	normalized, ok := normalizeBool(value)
	if !ok {
		return fallback
	}
	return normalized == "yes"
}

// RenderShare turns a validated request into the stanza that will be written.
//
// Only the parameters the form collected are written. A share this tool edits
// keeps every other parameter it had, because the stanza is replaced inside
// the file it lives in rather than regenerated from the effective
// configuration — which would write out every default Samba has and bury the
// three lines somebody actually chose.
func RenderShare(req shares.ShareRequest, keep []string) ([]string, error) {
	if err := CheckShareName(req.Name); err != nil {
		return nil, err
	}
	if err := CheckPath(req.Path); err != nil {
		return nil, err
	}
	comment := strings.TrimSpace(req.Comment)
	if err := checkValue("a comment", comment); err != nil {
		return nil, err
	}

	browseable, ok := normalizeBool(req.Browseable)
	if !ok {
		return nil, fmt.Errorf("samba: browseable is yes or no, not %q", req.Browseable)
	}
	readOnly, ok := normalizeBool(req.ReadOnly)
	if !ok {
		return nil, fmt.Errorf("samba: read only is yes or no, not %q", req.ReadOnly)
	}
	guest, ok := normalizeBool(req.GuestOK)
	if !ok {
		return nil, fmt.Errorf("samba: guest ok is yes or no, not %q", req.GuestOK)
	}

	validUsers, err := checkList("valid users", req.ValidUsers)
	if err != nil {
		return nil, err
	}
	writeList, err := checkList("the write list", req.WriteList)
	if err != nil {
		return nil, err
	}
	createMask, err := checkMask("create mask", req.CreateMask)
	if err != nil {
		return nil, err
	}
	directoryMask, err := checkMask("directory mask", req.DirectoryMask)
	if err != nil {
		return nil, err
	}

	lines := []string{"[" + strings.TrimSpace(req.Name) + "]"}
	add := func(key, value string) {
		if value != "" {
			lines = append(lines, "\t"+key+" = "+value)
		}
	}
	add("comment", comment)
	add("path", strings.TrimSpace(req.Path))
	add("browseable", browseable)
	add("read only", readOnly)
	add("guest ok", guest)
	add("valid users", strings.Join(validUsers, " "))
	add("write list", strings.Join(writeList, " "))
	add("create mask", createMask)
	add("directory mask", directoryMask)
	// Every other line the stanza already carried, in the order it had them.
	lines = append(lines, keep...)
	return lines, nil
}

// checkList validates an access list and returns it split.
func checkList(label, value string) ([]string, error) {
	var out []string
	for _, entry := range SplitList(value) {
		if err := CheckUser(entry); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		out = append(out, entry)
	}
	return out, nil
}

// checkMask validates a file mode, returning it in the four-digit form Samba
// prints.
func checkMask(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !maskRe.MatchString(value) {
		return "", fmt.Errorf("samba: %s is an octal mode like 0664, not %q",
			label, value)
	}
	if len(value) == 3 {
		return "0" + value, nil
	}
	return value, nil
}

// Header is the banner tui-samba writes above a file it created. An existing
// smb.conf never gets one: the file belongs to whoever wrote it, and a tool
// that stamped its name on somebody else's configuration would be claiming it.
const Header = "# Written by tui-samba. This file is included from smb.conf.\n"

// RenderDropIn is the whole text of a drop-in file for one share.
func RenderDropIn(stanza []string) string {
	return Header + strings.Join(stanza, "\n") + "\n"
}

// BuildValidate asks Samba's own parser to read a staged file.
//
// It is what runs before the user is asked to confirm anything: a parameter
// smbd would refuse is not something to discover after the file is in /etc,
// and testparm is the only thing on the machine that knows what Samba accepts.
func BuildValidate(staged string) (shares.Command, error) {
	if !stagedRe.MatchString(staged) {
		return shares.Command{}, fmt.Errorf("samba: %q is not a staging path", staged)
	}
	return shares.Command{
		Argv:        []string{BinTestparm, "-s", staged},
		Description: "Check the staged configuration with Samba's own parser",
	}, nil
}

// BuildInstall copies a staged file to its destination.
//
// `install` is used rather than `cp` because it sets the mode in the same call,
// so there is no window where the file is on disk with the wrong one.
func BuildInstall(staged, destination string) (shares.Command, error) {
	if !stagedRe.MatchString(staged) {
		return shares.Command{}, fmt.Errorf("samba: %q is not a staging path", staged)
	}
	if !validDestination(destination) {
		return shares.Command{}, fmt.Errorf(
			"samba: %q is neither %s nor a file in %s", destination, ConfigPath,
			DropInDir)
	}
	return shares.Command{
		Argv:        []string{"install", "-m", FileMode, staged, destination},
		Description: "Install " + staged + " as " + destination,
		Destructive: true,
	}, nil
}

// BuildMakeDropInDir creates the drop-in directory.
func BuildMakeDropInDir() shares.Command {
	return shares.Command{
		Argv:        []string{"install", "-d", "-m", DirMode, DropInDir},
		Description: "Create " + DropInDir,
		Destructive: true,
	}
}

// BuildReload tells every running smbd to re-read its configuration.
//
// A share change needs no restart, and that distinction is the reason this is
// the only command the tool sends the server. `reload-config` re-reads the
// files; every client that is connected stays connected, and a session that is
// mid-copy does not notice. Restarting smbd would drop all of them.
func BuildReload() shares.Command {
	return shares.Command{
		Argv: []string{BinSmbcontrol, "all", "reload-config"},
		Description: "Tell the running server to re-read its configuration; " +
			"connected clients stay connected",
	}
}

// BuildSelfTest asks the server for its own share list, the way a client
// would.
//
// `-N` is what makes it a test rather than a login: no password is sent and
// none is prompted for, so what comes back is what an anonymous client on the
// network can see — which is a more useful answer than the configuration,
// because it is the one the network gets.
func BuildSelfTest(host string) (shares.Command, error) {
	if host == "" {
		host = "localhost"
	}
	if !hostRe.MatchString(host) {
		return shares.Command{}, fmt.Errorf("samba: %q is not a host name", host)
	}
	return shares.Command{
		Argv: []string{BinSmbclient, "-L", host, "-N"},
		Description: "Ask " + host + " for its share list without sending a " +
			"password, the way an anonymous client sees it",
	}, nil
}

// hostRe accepts the host the self-test connects to. It is this machine in
// practice, and it is checked because it reaches an argv.
var hostRe = regexp.MustCompile(
	`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// BuildUserAdd adds an account to the Samba password database.
//
// The password goes on standard input and never into an argv, because a
// command line is visible in `ps` to every account on the machine. `-s` is
// what makes smbpasswd read it there: it prints no prompt and takes the new
// password and its confirmation as two lines.
//
// The Unix account has to exist first. Samba's database maps a name to a Unix
// uid, and an entry with nothing behind it is an account nobody can log in as
// — so the caller checks, and the message points at the tool for that job.
func BuildUserAdd(name, password string) (shares.Command, error) {
	if err := CheckUser(name); err != nil {
		return shares.Command{}, err
	}
	if err := CheckPassword(password); err != nil {
		return shares.Command{}, err
	}
	return shares.Command{
		Argv: []string{BinSmbpasswd, "-a", "-s", name},
		Description: "Add " + name + " to the Samba password database, reading " +
			"the password from standard input",
		Destructive: true,
		Stdin:       password + "\n" + password + "\n",
	}, nil
}

// BuildSetPassword replaces one account's Samba password, the same way.
func BuildSetPassword(name, password string) (shares.Command, error) {
	if err := CheckUser(name); err != nil {
		return shares.Command{}, err
	}
	if err := CheckPassword(password); err != nil {
		return shares.Command{}, err
	}
	return shares.Command{
		Argv: []string{BinSmbpasswd, "-s", name},
		Description: "Replace " + name + "'s Samba password, reading it from " +
			"standard input",
		Destructive: true,
		Stdin:       password + "\n" + password + "\n",
	}, nil
}

// userFlags maps an action to smbpasswd's own flag and the sentence that
// describes it.
var userFlags = map[string]struct {
	flag        string
	description string
	destructive bool
}{
	shares.UserDelete: {"-x",
		"Remove %s from the Samba password database; the Unix account is left alone",
		true},
	shares.UserEnable: {"-e",
		"Let %s log in again", false},
	shares.UserDisable: {"-d",
		"Stop %s logging in, without removing the account or its password", true},
}

// BuildUserAction is delete, enable and disable: one verb, one account.
func BuildUserAction(name, action string) (shares.Command, error) {
	if err := CheckUser(name); err != nil {
		return shares.Command{}, err
	}
	spec, ok := userFlags[action]
	if !ok {
		return shares.Command{}, fmt.Errorf(
			"samba: %q is not something tui-samba does to an account", action)
	}
	return shares.Command{
		Argv:        []string{BinSmbpasswd, spec.flag, name},
		Description: fmt.Sprintf(spec.description, name),
		Destructive: spec.destructive,
	}, nil
}

// ReplaceStanza produces the new text of a configuration file with one share's
// section replaced, added or removed.
//
// The file is rewritten section by section rather than regenerated, so every
// comment, every blank line and every other share survives untouched. That
// matters more here than it looks: an smb.conf is usually the distribution's
// own with three lines added to it, and a regenerated one would replace a
// documented file with a dump of Samba's defaults.
//
// A share that is not in the file is appended. body is the stanza's lines,
// header included; an empty body removes the section.
func ReplaceStanza(existing, name string, body []string) (string, error) {
	if err := CheckShareName(name); err != nil {
		return "", err
	}
	lines := splitLines(existing)
	start, end := findStanza(lines, name)

	var out []string
	switch {
	case start < 0 && len(body) == 0:
		return existing, nil
	case start < 0:
		out = append(out, lines...)
		// A file that does not end in a blank line would join the new section
		// onto the last one.
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, body...)
	default:
		out = append(out, lines[:start]...)
		out = append(out, body...)
		out = append(out, lines[end:]...)
	}

	text := strings.Join(out, "\n")
	if text != "" {
		text += "\n"
	}
	return text, nil
}

// AddInclude appends one `include =` line to the file's [global] section, or
// returns the text unchanged when the line is already there.
//
// It goes at the end of [global] rather than at the end of the file, because
// `include` is processed where it appears and a share defined after the
// section that includes it would be the one that wins. Putting it inside
// [global] is also what makes it survive somebody adding a share by hand
// afterwards.
func AddInclude(existing, line string) (string, error) {
	if err := checkValue("an include line", line); err != nil {
		return "", err
	}
	lines := splitLines(existing)
	for _, current := range lines {
		if strings.EqualFold(strings.TrimSpace(current), line) {
			return existing, nil
		}
	}
	start, end := findStanza(lines, "global")
	if start < 0 {
		// No [global] at all is a configuration nobody wrote, but it is one
		// this tool can still fix: the section is created with the line in it.
		lines = append(lines, "", "[global]", "\t"+line)
		return strings.Join(lines, "\n") + "\n", nil
	}
	// Back up over the blank lines the section ends with, so the new line
	// lands under the last parameter rather than after a gap.
	insert := end
	for insert > start+1 && strings.TrimSpace(lines[insert-1]) == "" {
		insert--
	}
	out := append([]string{}, lines[:insert]...)
	out = append(out, "\t"+line)
	out = append(out, lines[insert:]...)
	return strings.Join(out, "\n") + "\n", nil
}

// sectionRe matches a section header, whatever indentation it carries.
var sectionRe = regexp.MustCompile(`^\s*\[([^\]]*)\]\s*$`)

// findStanza returns the half-open line range one section occupies, or -1 when
// the file has no such section.
func findStanza(lines []string, name string) (start, end int) {
	start, end = -1, len(lines)
	for i, line := range lines {
		match := sectionRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if start >= 0 {
			return start, i
		}
		if strings.EqualFold(strings.TrimSpace(match[1]), name) {
			start = i
		}
	}
	if start < 0 {
		return -1, len(lines)
	}
	return start, end
}

// KeepLines are the stanza's own lines this tool does not touch: everything
// except the parameters the form collects, which are rewritten from it.
//
// It is what makes an edit an edit. A share with `vfs objects = recycle` keeps
// it; a share with a `hosts allow` keeps that too, and the form never has to
// grow a field for every parameter Samba has in order to be safe to use.
func KeepLines(stanza []string) []string {
	managed := map[string]bool{
		"comment": true, "path": true, "browseable": true, "browsable": true,
		"read only": true, "writeable": true, "writable": true,
		"guest ok": true, "public": true,
		"valid users": true, "write list": true,
		"create mask": true, "create mode": true,
		"directory mask": true, "directory mode": true,
	}
	var kept []string
	for _, line := range stanza {
		if sectionRe.MatchString(line) {
			continue
		}
		key, _, found := strings.Cut(line, "=")
		if !found {
			// A comment or a blank line inside the stanza: keep it as it is.
			if strings.TrimSpace(line) != "" {
				kept = append(kept, line)
			}
			continue
		}
		if managed[normalizeKey(key)] {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// normalizeKey is how Samba reads a parameter name: case and internal spaces
// are both insignificant, so `ReadOnly`, `read only` and `readonly` are one
// parameter. The model keys on the spaced, lower-cased form, which is the one
// testparm prints.
func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Join(strings.Fields(key), " ")
}

// splitLines splits a file into lines without the empty element a trailing
// newline produces.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// firstLine keeps a message to a single line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
