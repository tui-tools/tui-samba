package samba

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/tui-tools/tui-samba/internal/shares"
)

// This file turns what the Samba programs print into the model. Every function
// here is a pure function of a string: no clock, no filesystem, no process, so
// each one is covered by a fixture of real output in testdata.

// ParseTestparm reads the effective configuration `testparm -s` dumps.
//
// The dump is the whole point of using testparm rather than reading smb.conf:
// it is the configuration *as the server resolved it*, with every include
// expanded, every synonym normalised and every default filled in. A tool that
// parsed smb.conf itself would show what somebody typed, and what somebody
// typed is exactly what they are asking about when a share does not behave.
//
// testparm writes its diagnostics and the dump to different streams and the
// runner merges them, so the leading chatter — "Load smb config files from …",
// "Loaded services file OK.", the server role — is read for what it carries
// and then skipped.
func ParseTestparm(out string) (shares.Global, []shares.Share) {
	global := shares.Global{Params: map[string]string{}}
	var list []shares.Share
	var current *shares.Share
	// skipping is set inside a stanza whose parameters belong to nothing this
	// tool models, so they are dropped rather than folded into the globals.
	skipping := false

	flush := func() {
		if current != nil {
			list = append(list, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "Load smb config files from "); ok {
			global.ConfigFile = strings.TrimSpace(rest)
			continue
		}
		if match := sectionRe.FindStringSubmatch(line); match != nil {
			flush()
			name := strings.TrimSpace(match[1])
			if strings.EqualFold(name, "global") {
				skipping = false
				continue
			}
			// A stanza with no name between the brackets is not a share, and
			// listing one would put a share on the screen that the editor
			// could never write back. Its parameters are not global either.
			if name == "" {
				skipping = true
				continue
			}
			skipping = false
			current = &shares.Share{
				Name:    name,
				Special: IsSpecial(name),
				Params:  map[string]string{},
				Raw:     []string{"[" + name + "]"},
			}
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			// A diagnostic line, or the "# Global parameters" banner.
			continue
		}
		if skipping {
			continue
		}
		key = normalizeKey(key)
		if key == "" {
			// A line that is only "=" names no parameter.
			continue
		}
		value = strings.TrimSpace(value)
		if current == nil {
			global.Params[key] = value
			continue
		}
		current.Params[key] = value
		current.Raw = append(current.Raw, "\t"+key+" = "+value)
	}
	flush()

	applyGlobal(&global)
	for i := range list {
		applyShare(&list[i], global)
	}
	return global, list
}

// applyGlobal lifts the global parameters the UI reads by name out of the map.
func applyGlobal(global *shares.Global) {
	get := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := global.Params[key]; ok {
				return value
			}
		}
		return ""
	}
	global.Workgroup = get("workgroup")
	global.ServerString = get("server string")
	global.Security = strings.ToUpper(get("security"))
	global.MapToGuest = get("map to guest")
	global.MinProtocol = get("server min protocol", "min protocol", "server min protocol")
	global.MaxProtocol = get("server max protocol", "max protocol")
	global.SMB1Enabled = IsSMB1(global.MinProtocol)
}

// dialectRank orders the SMB dialects Samba names, so "is this below SMB2" is
// a comparison rather than a list of strings to keep in step with Samba.
var dialectRank = map[string]int{
	"CORE": 0, "COREPLUS": 1, "LANMAN1": 2, "LANMAN2": 3,
	"NT1": 4, "SMB1": 4,
	"SMB2": 5, "SMB2_02": 5, "SMB2_10": 6, "SMB2_22": 7, "SMB2_24": 8,
	"SMB3": 9, "SMB3_00": 9, "SMB3_02": 10, "SMB3_10": 11, "SMB3_11": 12,
}

// IsSMB1 reports whether a minimum-protocol value lets a client speak SMB1.
//
// An empty value is not SMB1. Since Samba 4.11 the default minimum is SMB2_02,
// and a machine that never set the parameter is a machine on that default —
// reporting the finding there would be reporting it on nearly every server.
func IsSMB1(minProtocol string) bool {
	value := strings.ToUpper(strings.TrimSpace(minProtocol))
	if value == "" {
		return false
	}
	rank, known := dialectRank[value]
	if !known {
		return false
	}
	return rank < dialectRank["SMB2_02"]
}

// applyShare lifts the share parameters the UI reads by name out of the map.
func applyShare(share *shares.Share, global shares.Global) {
	get := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := share.Params[key]; ok {
				return value
			}
		}
		return ""
	}
	// A parameter a share does not set is inherited from [global], which is
	// how Samba itself resolves it. testparm prints only what differs from the
	// default, so a share that says nothing about `guest ok` really is on
	// whatever [global] says.
	inherit := func(key string, fallback bool) bool {
		if value, ok := share.Params[key]; ok {
			return Bool(value, fallback)
		}
		if value, ok := global.Params[key]; ok {
			return Bool(value, fallback)
		}
		return fallback
	}

	share.Path = get("path", "directory")
	share.Comment = get("comment")
	share.Browseable = inherit("browseable", true)
	if value := get("browsable"); value != "" {
		share.Browseable = Bool(value, share.Browseable)
	}
	// `read only` and `writeable` are the same parameter with opposite senses,
	// and a configuration may carry either spelling.
	share.ReadOnly = inherit("read only", true)
	for _, key := range []string{"writeable", "writable", "write ok"} {
		if value, ok := share.Params[key]; ok {
			share.ReadOnly = !Bool(value, false)
		}
	}
	share.GuestOK = inherit("guest ok", false)
	if value := get("public"); value != "" {
		share.GuestOK = Bool(value, share.GuestOK)
	}
	share.ValidUsers = SplitList(get("valid users"))
	share.WriteList = SplitList(get("write list"))
	share.CreateMask = get("create mask", "create mode")
	share.DirectoryMask = get("directory mask", "directory mode")
	share.InheritPermissions = Bool(get("inherit permissions"), false)
	share.VFSObjects = SplitList(get("vfs objects", "vfs object"))
}

// ParseIncludes reads the `include =` lines out of a raw smb.conf.
//
// They cannot come from testparm: an include is expanded before the effective
// configuration is dumped, so the dump is the one place on the machine where
// the include is invisible. Whether the main file already reaches this tool's
// drop-in directory is a question only the raw file answers.
func ParseIncludes(raw string) []string {
	var out []string
	for _, line := range splitLines(raw) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found || normalizeKey(key) != "include" {
			continue
		}
		if value := strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// ParseServerVersion reads the version out of `smbd --version`, which prints
// "Version 4.20.5" and nothing else.
func ParseServerVersion(out string) string {
	line := strings.TrimSpace(firstLine(out))
	if rest, ok := strings.CutPrefix(line, "Version "); ok {
		return strings.TrimSpace(rest)
	}
	return line
}

// pdbeditSeparator is the line pdbedit prints between two accounts.
const pdbeditSeparator = "---------------"

// flagsRe pulls the account flags out of the "[U          ]" field.
var flagsRe = regexp.MustCompile(`\[([A-Za-z ]*)\]`)

// ParsePdbedit reads the accounts `pdbedit -L -v` lists.
//
// The verbose form is used rather than the one-line one because the one-line
// form carries no flags, and the flags are the whole point: an account that is
// in the database and disabled looks exactly like one that works, right up
// until somebody tries it.
func ParsePdbedit(out string) []shares.User {
	var list []shares.User
	var current map[string]string

	flush := func() {
		if current == nil {
			return
		}
		if name := current["unix username"]; name != "" {
			list = append(list, userFrom(current))
		}
		current = nil
	}

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, pdbeditSeparator) {
			flush()
			current = map[string]string{}
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		if current == nil {
			current = map[string]string{}
		}
		current[normalizeKey(key)] = strings.TrimSpace(value)
	}
	flush()
	return list
}

// userFrom builds one account from the fields pdbedit printed.
func userFrom(fields map[string]string) shares.User {
	user := shares.User{
		Name:            fields["unix username"],
		UID:             -1,
		PasswordLastSet: fields["password last set"],
		HomeDirectory:   fields["home directory"],
	}
	if match := flagsRe.FindStringSubmatch(fields["account flags"]); match != nil {
		user.Flags = strings.TrimSpace(match[1])
	}
	// The flags are single letters in a padded field: D is disabled, N is "no
	// password required", which is the one worth as much noise as a disabled
	// account and gets none from Samba itself.
	user.Disabled = strings.Contains(user.Flags, "D")
	user.NoPassword = strings.Contains(user.Flags, "N")
	return user
}

// ParseStatusJSON reads `smbstatus --json`.
//
// It is the better of the two paths and the only one that carries the
// per-session encryption and signing state. It arrived in Samba 4.17, so the
// text parser below is what every older server is read with.
func ParseStatusJSON(out string) ([]shares.Session, []shares.TreeConnect,
	[]shares.OpenFile, error) {
	// smbstatus prints its own warnings before the document on some builds, so
	// the parse starts at the first brace rather than at byte zero.
	start := strings.IndexByte(out, '{')
	if start < 0 {
		return nil, nil, nil, fmt.Errorf("samba: smbstatus printed no JSON document")
	}

	var report statusReport
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		return nil, nil, nil, fmt.Errorf("samba: smbstatus JSON: %w", err)
	}

	sessions := make([]shares.Session, 0, len(report.Sessions))
	for _, session := range report.Sessions {
		sessions = append(sessions, shares.Session{
			PID:        session.ServerID.PID,
			User:       session.Username,
			Group:      session.Groupname,
			Machine:    session.RemoteMachine,
			Remote:     session.Hostname,
			Protocol:   session.SessionDialect,
			Encryption: session.Encryption.String(),
			Signing:    session.Signing.String(),
			SessionID:  session.SessionID,
		})
	}

	connections := make([]shares.TreeConnect, 0, len(report.Tcons))
	for _, tcon := range report.Tcons {
		connections = append(connections, shares.TreeConnect{
			Service:    tcon.Service,
			PID:        tcon.ServerID.PID,
			Machine:    tcon.Machine,
			Since:      tcon.ConnectedAt,
			Encryption: tcon.Encryption.String(),
			Signing:    tcon.Signing.String(),
		})
	}

	// A byte-range lock is keyed on the file it is held over, so the set of
	// locked paths is what turns "open" into "in use" on the screen.
	locked := map[string]bool{}
	for _, lock := range report.ByteRangeLocks {
		locked[path.Join(lock.SharePath, lock.FileName)] = true
	}

	var files []shares.OpenFile
	for key, file := range report.OpenFiles {
		full := key
		if file.ServicePath != "" && file.Filename != "" {
			full = path.Join(file.ServicePath, file.Filename)
		}
		for _, open := range file.Opens {
			files = append(files, shares.OpenFile{
				PID:    open.ServerID.PID,
				User:   open.Username,
				Path:   full,
				Access: open.AccessMask.Text,
				Oplock: open.Oplock.Text,
				Locked: locked[full],
			})
		}
	}

	sortSessions(sessions)
	sortConnections(connections)
	sortFiles(files)
	return sessions, connections, files, nil
}

// statusReport is the subset of `smbstatus --json` this tool reads. The key
// names are Samba's own, from source3/utils/status_json.c.
type statusReport struct {
	Version        string                    `json:"version"`
	SMBConf        string                    `json:"smb_conf"`
	Sessions       map[string]jsonSession    `json:"sessions"`
	Tcons          map[string]jsonTcon       `json:"tcons"`
	OpenFiles      map[string]jsonOpenFile   `json:"open_files"`
	ByteRangeLocks map[string]jsonRangeLocks `json:"byte_range_locks"`
}

// jsonServerID is the process behind a session, a connection or an open file.
// The pid is a string in Samba's output, not a number.
type jsonServerID struct {
	PID string `json:"pid"`
}

// jsonCrypto is the shape both `encryption` and `signing` have.
type jsonCrypto struct {
	Cipher string `json:"cipher"`
	Degree string `json:"degree"`
}

// String is what the screen shows: the cipher when there is one, and the
// degree otherwise — because "none" is the answer, and an empty cell would
// read as a value nobody managed to read.
func (c jsonCrypto) String() string {
	if c.Cipher != "" {
		return c.Cipher
	}
	if c.Degree != "" {
		return c.Degree
	}
	return "-"
}

type jsonSession struct {
	SessionID      string       `json:"session_id"`
	ServerID       jsonServerID `json:"server_id"`
	Username       string       `json:"username"`
	Groupname      string       `json:"groupname"`
	RemoteMachine  string       `json:"remote_machine"`
	Hostname       string       `json:"hostname"`
	SessionDialect string       `json:"session_dialect"`
	Encryption     jsonCrypto   `json:"encryption"`
	Signing        jsonCrypto   `json:"signing"`
}

type jsonTcon struct {
	Service     string       `json:"service"`
	ServerID    jsonServerID `json:"server_id"`
	Machine     string       `json:"machine"`
	ConnectedAt string       `json:"connected_at"`
	Encryption  jsonCrypto   `json:"encryption"`
	Signing     jsonCrypto   `json:"signing"`
}

type jsonOpenFile struct {
	ServicePath string              `json:"service_path"`
	Filename    string              `json:"filename"`
	Opens       map[string]jsonOpen `json:"opens"`
}

// jsonMask is the shape of `sharemode`, `access_mask`, `caching` and `oplock`:
// a flag per bit plus a `text` summary, which is the only part worth showing.
// An oplock nobody holds is an empty object, so `text` is simply absent.
type jsonMask struct {
	Text string `json:"text"`
}

type jsonOpen struct {
	ServerID jsonServerID `json:"server_id"`
	// Username is absent on Samba 4.17, which reports only the uid; a blank
	// user column is the honest answer there.
	Username   string   `json:"username"`
	UID        int      `json:"uid"`
	AccessMask jsonMask `json:"access_mask"`
	Oplock     jsonMask `json:"oplock"`
	OpenedAt   string   `json:"opened_at"`
}

type jsonRangeLocks struct {
	FileName  string `json:"file_name"`
	SharePath string `json:"share_path"`
}

// The headers that open each section of `smbstatus` text output. They are what
// the text parser keys on, because the columns themselves are separated by
// runs of spaces a value may also contain.
const (
	sessionHeader = "PID"
	tconHeader    = "Service"
	lockedHeader  = "Locked files:"
)

// sessionLineRe reads one session row of the text output.
//
// It is anchored on the dialect token rather than counting columns, because
// the machine column carries a host name, a space and a bracketed address —
// "127.0.0.1 (ipv4:127.0.0.1:59944)" — and no amount of splitting on
// whitespace will keep that in one piece.
var sessionLineRe = regexp.MustCompile(
	`^(\d+)\s+(\S+)\s+(\S+)\s+(\S.*?)\s+(SMB[0-9_]*|NT1|LANMAN[0-9]|CORE(?:PLUS)?|unknown)\s+(\S+)\s+(\S+)\s*$`)

// tconLineRe reads one tree-connect row: the share, the process, the machine
// and everything after it, which is the timestamp and the two crypto columns.
var tconLineRe = regexp.MustCompile(`^(\S+)\s+(\d+)\s+(\S+)\s+(\S.*)$`)

// lockedLineRe reads one locked-file row. The two columns that matter are the
// share path and the name under it, which are the last two before the
// timestamp.
var lockedLineRe = regexp.MustCompile(
	`^(\d+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(/\S*)\s+(\S+)\s+(\S.*)$`)

// machineRe splits "host (ipv4:addr:port)" into the two halves smbstatus
// prints there.
var machineRe = regexp.MustCompile(`^(\S+)\s+\((.*)\)$`)

// ParseStatusText reads plain `smbstatus` output, which is what a server older
// than 4.17 has.
//
// It is deliberately conservative. The text output is columns of runs of
// spaces with values that contain spaces in them, so a row this parser cannot
// read with confidence is skipped rather than guessed at — a session invented
// out of a misread line would be worse than a session the screen does not
// show, and the screen says which server could not answer in JSON.
func ParseStatusText(out string) ([]shares.Session, []shares.TreeConnect,
	[]shares.OpenFile) {
	var sessions []shares.Session
	var connections []shares.TreeConnect
	var files []shares.OpenFile

	section := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "-----"):
			continue
		case strings.HasPrefix(trimmed, lockedHeader):
			section = "locked"
			continue
		case strings.HasPrefix(trimmed, sessionHeader+" "):
			section = "sessions"
			continue
		case strings.HasPrefix(trimmed, tconHeader+" "):
			section = "tcons"
			continue
		case strings.HasPrefix(trimmed, "Pid "):
			// The locked-files column header.
			continue
		case strings.HasPrefix(trimmed, "Samba version"):
			continue
		case strings.HasPrefix(trimmed, "No locked files"):
			continue
		}

		switch section {
		case "sessions":
			if match := sessionLineRe.FindStringSubmatch(trimmed); match != nil {
				machine, remote := splitMachine(match[4])
				sessions = append(sessions, shares.Session{
					PID: match[1], User: match[2], Group: match[3],
					Machine: machine, Remote: remote,
					Protocol: match[5], Encryption: match[6], Signing: match[7],
				})
			}
		case "tcons":
			if match := tconLineRe.FindStringSubmatch(trimmed); match != nil {
				connections = append(connections, shares.TreeConnect{
					Service: match[1], PID: match[2], Machine: match[3],
					Since: strings.TrimSpace(match[4]),
				})
			}
		case "locked":
			if match := lockedLineRe.FindStringSubmatch(trimmed); match != nil {
				files = append(files, shares.OpenFile{
					PID:    match[1],
					User:   match[2],
					Access: match[5],
					Oplock: match[6],
					Path:   path.Join(match[7], match[8]),
					Locked: true,
				})
			}
		}
	}

	sortSessions(sessions)
	sortConnections(connections)
	sortFiles(files)
	return sessions, connections, files
}

// splitMachine separates "host (ipv4:addr:port)" into the name and the
// address, and leaves a value in one piece when it is not in that shape.
func splitMachine(value string) (machine, remote string) {
	value = strings.TrimSpace(value)
	if match := machineRe.FindStringSubmatch(value); match != nil {
		return match[1], match[2]
	}
	return value, ""
}

// ParseProperties reads the `Key=Value` lines `systemctl show` prints.
func ParseProperties(out string) map[string]string {
	properties := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || key == "" {
			continue
		}
		properties[key] = value
	}
	return properties
}

// smbPorts are the sockets a file server answers on: 445 is SMB over TCP and
// 139 is the NetBIOS session service, which is only there when nmbd is
// running.
var smbPorts = map[int]bool{139: true, 445: true}

// listenRe reads one row of `ss -tlnp`. The address and port are one field
// separated by a colon, which for an IPv6 address is the last colon of
// several.
var listenRe = regexp.MustCompile(`^LISTEN\s+\S+\s+\S+\s+(\S+):(\d+)\s+(.*)$`)

// processRe pulls the program name out of the `users:(("smbd",pid=1,fd=2))`
// field, which is only populated for a reader allowed to see it.
var processRe = regexp.MustCompile(`users:\(\("([^"]+)"`)

// ParseListening reads `ss -tlnp` and keeps the two ports a file server is
// reached on.
//
// It is a read the tool makes to answer one question the configuration cannot:
// whether anything is actually listening. A perfectly good smb.conf on a
// machine whose smbd is not running looks the same on paper as one that works.
func ParseListening(out string) []shares.Port {
	var ports []shares.Port
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		match := listenRe.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		number, err := strconv.Atoi(match[2])
		if err != nil || !smbPorts[number] {
			continue
		}
		port := shares.Port{Port: number, Address: match[1]}
		if process := processRe.FindStringSubmatch(match[3]); process != nil {
			port.Process = process[1]
		}
		key := port.Address + ":" + match[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		ports = append(ports, port)
	}
	return ports
}

// ParseBooleans reads `getsebool a b`, which prints one "name --> on" line per
// boolean it was asked about.
func ParseBooleans(out string) map[string]bool {
	values := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name, state, found := strings.Cut(strings.TrimSpace(line), "-->")
		if !found {
			continue
		}
		// A line with nothing before the arrow names no boolean, and an entry
		// under a blank name is one no caller can ever ask for.
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		values[name] = strings.TrimSpace(state) == "on"
	}
	return values
}

// ParseStat reads `stat -c %a %U %G %F` output: the mode in octal, the owner,
// the group and the file type in words.
func ParseStat(out string) (shares.DirInfo, error) {
	fields := strings.Fields(strings.TrimSpace(firstLine(out)))
	if len(fields) < 3 {
		return shares.DirInfo{}, fmt.Errorf("samba: %q is not a stat line", out)
	}
	mode, err := strconv.ParseUint(fields[0], 8, 32)
	if err != nil {
		return shares.DirInfo{}, fmt.Errorf("samba: %q is not an octal mode",
			fields[0])
	}
	info := shares.DirInfo{
		Exists:        true,
		Mode:          PadMode(fields[0]),
		Owner:         fields[1],
		Group:         fields[2],
		WorldWritable: mode&0o002 != 0,
		IsDir:         strings.Contains(strings.Join(fields[3:], " "), "directory"),
	}
	return info, nil
}

// PadMode renders an octal mode the way `ls` and every document about Samba
// write one: four digits, so the setgid bit a group share needs is visible.
func PadMode(value string) string {
	value = strings.TrimSpace(value)
	for len(value) < 4 {
		value = "0" + value
	}
	return value
}

// contextRe pulls the SELinux label out of `ls -Zd` output, which prints the
// context and then the name. A label is four colon-separated fields — user,
// role, type, level — and the type is the one that decides whether Samba may
// read the directory at all.
var contextRe = regexp.MustCompile(
	`([a-z_]+_u:[a-z_]+_r:[a-z_]+_t:[A-Za-z0-9:.,_-]+)`)

// ParseContext reads the SELinux label `ls -Zd` printed for a path.
func ParseContext(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if match := contextRe.FindStringSubmatch(line); match != nil {
			return match[1]
		}
	}
	return ""
}
