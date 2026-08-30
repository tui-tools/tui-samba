package main

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tui-tools/tui-kit/ui"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// Layout constants: the rows the table cannot use.
const (
	headerLines = 2
	footerLines = 2
	// tabLines is the one row the tab bar takes.
	tabLines = 1
	// minTableHeight keeps at least one visible row on a very short terminal.
	minTableHeight = 1
	// dialogDiffLines bounds the diff shown above a command preview, so the
	// commands themselves are never pushed off a short terminal.
	dialogDiffLines = 14
)

// tableHeight is the number of rows that fit on screen.
func (a *app) tableHeight() int {
	// header + tabs + table header + footer + status line.
	return max(a.height-headerLines-tabLines-footerLines-2, minTableHeight)
}

// detailHeight is the number of detail lines that fit on screen.
func (a *app) detailHeight() int {
	return max(a.height-headerLines-tabLines-footerLines-1, minTableHeight)
}

// View renders the whole screen.
func (a *app) View() string {
	switch a.mode {
	case modeConfirm:
		return a.confirm.View(a.theme, a.width, a.height)
	case modeInput:
		return a.input.View(a.theme, a.width, a.height)
	case modePicker:
		return a.picker.View(a.theme, a.width, a.height)
	case modeForm:
		return a.form.view(a.theme, a.width, a.height)
	case modeHelp:
		return placeCenter(
			ui.HelpScreen(a.theme, "tui-samba — keys", helpKeys(), a.width),
			a.width, a.height)
	case modeDetail:
		return a.detailView()
	}
	return a.browseView()
}

// placeCenter centers a rendered box in the terminal.
func placeCenter(box string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// browseView renders a screen: header, tab bar, table, help bar, status.
func (a *app) browseView() string {
	header := a.headerView()
	tabs := a.tabsView()

	var body string
	switch {
	case a.loading && a.rowCount() == 0:
		body = ui.EmptyState(a.theme, "reading this file server…", a.width,
			a.tableHeight()+1)
	case a.rowCount() == 0 && a.filter != "":
		body = ui.EmptyState(a.theme, "nothing matches "+strconv.Quote(a.filter),
			a.width, a.tableHeight()+1)
	case a.rowCount() == 0 && a.loadFailed:
		body = ui.EmptyState(a.theme,
			"this machine could not be read — see the message below",
			a.width, a.tableHeight()+1)
	case a.rowCount() == 0:
		body = ui.EmptyState(a.theme, a.emptyMessage(), a.width, a.tableHeight()+1)
	default:
		body = a.table()
	}

	help := ui.HelpBar(a.theme, a.shortHelpKeys(), a.width)
	status := ui.StatusLine(a.theme, a.statusKind, a.status, a.defaultStatus(),
		a.width)
	return strings.Join([]string{header, tabs, body, help, status}, "\n")
}

// emptyMessage is what a screen with no rows says, which is different on each.
func (a *app) emptyMessage() string {
	if !a.model.Installed {
		return orNone(a.model.Detail)
	}
	switch a.screen {
	case screenUsers:
		if a.model.UsersDetail != "" {
			return a.model.UsersDetail
		}
		return "no account is in the Samba password database — press a to add one"
	case screenConnections:
		if a.model.StatusDetail != "" {
			return a.model.StatusDetail
		}
		return "nobody is connected right now"
	case screenServer:
		return "nothing could be read about the server itself"
	default:
		if a.model.ConfigDetail != "" {
			return a.model.ConfigDetail
		}
		return "this server exports no share (press n to add one)"
	}
}

// headerView renders the facts at the top of every screen.
func (a *app) headerView() string {
	t := a.theme
	counts := a.model.Count()

	facts := []ui.Fact{{Label: "shares", Value: strconv.Itoa(counts.Shares)}}
	if counts.Guest > 0 {
		style := t.Warn
		facts = append(facts, ui.Fact{Label: "guest",
			Value: strconv.Itoa(counts.Guest), Style: &style})
	}
	if counts.Findings > 0 {
		style := t.Warn
		if counts.Risks > 0 {
			style = t.Danger
		}
		facts = append(facts, ui.Fact{Label: "findings",
			Value: strconv.Itoa(counts.Findings), Style: &style})
	}
	facts = append(facts,
		ui.Fact{Label: "accounts", Value: strconv.Itoa(counts.Users)},
		ui.Fact{Label: "sessions", Value: strconv.Itoa(counts.Sessions)})

	// Whether anything is actually serving is the question behind half of what
	// is on the other screens, so it is a header fact rather than a screen
	// nobody opens.
	if value, style := a.servingFact(); value != "" {
		facts = append(facts, ui.Fact{Label: "smbd", Value: value, Style: style})
	}
	// The backend version, when it was probed: quiet on a tested version,
	// coloured on one nobody has run against.
	if a.backendCompat.Backend != "" {
		facts = append(facts, ui.CompatFact(t, a.backendCompat))
	}

	subtitle := a.backend.Describe()
	if a.filter != "" {
		subtitle += "  ·  filter: " + a.filter
	}
	return ui.Header{Title: "tui-samba", Subtitle: subtitle, Facts: facts}.
		Render(t, a.width)
}

// servingFact summarises the file server in the two words a header has room
// for.
func (a *app) servingFact() (string, *lipgloss.Style) {
	for _, service := range a.model.Services {
		if service.Role != shares.RoleFileServer {
			continue
		}
		switch {
		case service.Active:
			style := a.theme.OK
			return "running", &style
		case service.Present:
			style := a.theme.Danger
			return orNone(service.State), &style
		}
	}
	style := a.theme.Muted
	return "no unit", &style
}

// tabsView renders the four screens as one row, with the current one accented.
func (a *app) tabsView() string {
	var parts []string
	for s := screen(0); s < screenCount; s++ {
		label := strconv.Itoa(int(s)+1) + " " + s.title()
		if s == a.screen {
			parts = append(parts, a.theme.Accent.Render("["+label+"]"))
			continue
		}
		parts = append(parts, a.theme.Muted.Render(" "+label+" "))
	}
	return a.theme.Footer.Width(a.width).Render(
		ui.Truncate(strings.Join(parts, " "), a.width-2))
}

// defaultStatus is the hint shown when there is no message to report.
func (a *app) defaultStatus() string {
	count := strconv.Itoa(a.rowCount())
	suffix := "  ·  tab to move  ·  ? for help"
	switch a.screen {
	case screenUsers:
		return count + " accounts  ·  a adds, p sets a password, D disables" + suffix
	case screenConnections:
		return count + " rows  ·  R re-reads" + suffix
	case screenServer:
		return count + " facts  ·  r reloads, t asks what a client sees" + suffix
	default:
		return count + " shares  ·  e edits, n adds" + suffix
	}
}

// table renders the current screen's rows.
func (a *app) table() string {
	columns, rows, styles := a.tableData()
	return ui.Table{
		Columns:  columns,
		Rows:     rows,
		Styles:   styles,
		Selected: a.cursor[a.screen],
		Offset:   a.offset[a.screen],
		Height:   a.tableHeight(),
	}.Render(a.theme, a.width)
}

// tableData builds the columns, cells and row styles of the current screen.
// Every screen drops its widest columns first on a narrow terminal, which is
// what keeps a 40-column pane readable.
func (a *app) tableData() ([]ui.Column, [][]string, []*lipgloss.Style) {
	switch a.screen {
	case screenUsers:
		return a.usersTable()
	case screenConnections:
		return a.connectionsTable()
	case screenServer:
		return a.serverTable()
	default:
		return a.sharesTable()
	}
}

// sharesTable is the inventory: what is exported, who may reach it, and what
// is wrong with it.
func (a *app) sharesTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "SHARE", Width: 14, Flex: true},
		{Title: "ACCESS", Width: 8},
		{Title: "", Width: 3},
	}
	showPath := a.width >= 60
	showMode := a.width >= 82
	showWho := a.width >= 100
	if showPath {
		columns = append(columns, ui.Column{Title: "PATH", Width: 22, Flex: true})
	}
	if showMode {
		columns = append(columns, ui.Column{Title: "MODE", Width: 6})
	}
	if showWho {
		columns = append(columns, ui.Column{Title: "WHO", Width: 18, Flex: true})
	}

	rows := make([][]string, 0, len(a.shareRows))
	styles := make([]*lipgloss.Style, 0, len(a.shareRows))
	for _, share := range a.shareRows {
		row := []string{share.Name, accessCell(share), verdictMark(share.Verdict)}
		if showPath {
			row = append(row, orNone(share.Path))
		}
		if showMode {
			row = append(row, modeCell(share))
		}
		if showWho {
			row = append(row, ui.Truncate(share.Access(), 40))
		}
		rows = append(rows, row)
		styles = append(styles, a.verdictStyle(share.Verdict))
	}
	return columns, rows, styles
}

// accessCell is the one word that says what a client can do here.
func accessCell(share shares.Share) string {
	switch {
	case share.GuestOK && share.Writable():
		return "guest rw"
	case share.GuestOK:
		return "guest ro"
	case share.Writable():
		return "rw"
	default:
		return "ro"
	}
}

// modeCell is the Unix mode of the exported directory, or why there is none.
func modeCell(share shares.Share) string {
	switch {
	case share.Path == "":
		return "—"
	case !share.Dir.Exists:
		return "gone"
	default:
		return share.Dir.Mode
	}
}

// usersTable is the Samba password database.
func (a *app) usersTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "ACCOUNT", Width: 16, Flex: true},
		{Title: "STATE", Width: 9},
	}
	showUID := a.width >= 60
	showSet := a.width >= 86
	if showUID {
		columns = append(columns, ui.Column{Title: "UID", Width: 7})
	}
	if showSet {
		columns = append(columns,
			ui.Column{Title: "PASSWORD SET", Width: 30, Flex: true})
	}

	rows := make([][]string, 0, len(a.userRows))
	styles := make([]*lipgloss.Style, 0, len(a.userRows))
	for _, user := range a.userRows {
		row := []string{user.Name, userState(user)}
		if showUID {
			row = append(row, uidCell(user))
		}
		if showSet {
			row = append(row, orNone(user.PasswordLastSet))
		}
		rows = append(rows, row)
		styles = append(styles, a.verdictStyle(userVerdict(user)))
	}
	return columns, rows, styles
}

// userState is the one word the accounts screen shows.
func userState(user shares.User) string {
	switch {
	case !user.UnixPresent:
		return "no unix"
	case user.Disabled:
		return "disabled"
	case user.NoPassword:
		return "no pass"
	default:
		return "enabled"
	}
}

// userVerdict paints an account the way a share is painted.
func userVerdict(user shares.User) shares.Verdict {
	switch {
	case !user.UnixPresent, user.NoPassword:
		return shares.VerdictRisk
	case user.Disabled:
		return shares.VerdictWarn
	default:
		return shares.VerdictOK
	}
}

// uidCell renders a uid, or a dash when there is none to render.
func uidCell(user shares.User) string {
	if user.UID < 0 {
		return "—"
	}
	return strconv.Itoa(user.UID)
}

// connectionsTable is who is on the server, with their shares and files under
// them.
func (a *app) connectionsTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "WHO", Width: 24, Flex: true},
		{Title: "FROM", Width: 16},
	}
	showWhere := a.width >= 66
	showDetail := a.width >= 96
	if showWhere {
		columns = append(columns, ui.Column{Title: "STATE", Width: 12})
	}
	if showDetail {
		columns = append(columns, ui.Column{Title: "", Width: 30, Flex: true})
	}

	rows := make([][]string, 0, len(a.connRows))
	styles := make([]*lipgloss.Style, 0, len(a.connRows))
	for _, row := range a.connRows {
		cells := []string{row.who, row.what}
		if showWhere {
			cells = append(cells, row.where)
		}
		if showDetail {
			cells = append(cells, row.detail)
		}
		rows = append(rows, cells)

		style := a.theme.Row
		if row.kind != rowSession {
			style = a.theme.Row.Foreground(a.theme.Muted.GetForeground())
		}
		styles = append(styles, &style)
	}
	return columns, rows, styles
}

// serverTable is the server itself: what is running, what is listening, and
// what is worth knowing about the configuration as a whole.
func (a *app) serverTable() ([]ui.Column, [][]string, []*lipgloss.Style) {
	columns := []ui.Column{
		{Title: "", Width: 16},
		{Title: "", Width: 40, Flex: true},
	}
	rows := make([][]string, 0, len(a.serverRow))
	styles := make([]*lipgloss.Style, 0, len(a.serverRow))
	for _, row := range a.serverRow {
		rows = append(rows, []string{row.label, row.value})
		style := a.theme.Row
		switch {
		case row.bad:
			style = a.theme.Row.Foreground(a.theme.Danger.GetForeground())
		case row.warn:
			style = a.theme.Row.Foreground(a.theme.Warn.GetForeground())
		}
		styles = append(styles, &style)
	}
	return columns, rows, styles
}

// verdictMark is the one-glyph verdict column. It is a symbol rather than a
// word because the column has to survive a 40-column terminal, and it is
// backed by the colour of the row for anyone who cannot tell them apart.
func verdictMark(verdict shares.Verdict) string {
	switch verdict {
	case shares.VerdictRisk:
		return "!!"
	case shares.VerdictWarn:
		return "!"
	case shares.VerdictOK:
		return "ok"
	default:
		return ""
	}
}

// verdictStyle colours a row by its verdict, so what is handing out too much
// stands out from what is merely exported.
func (a *app) verdictStyle(verdict shares.Verdict) *lipgloss.Style {
	var style lipgloss.Style
	switch verdict {
	case shares.VerdictRisk:
		style = a.theme.Row.Foreground(a.theme.Danger.GetForeground())
	case shares.VerdictWarn:
		style = a.theme.Row.Foreground(a.theme.Warn.GetForeground())
	case shares.VerdictOK:
		style = a.theme.Row.Foreground(a.theme.OK.GetForeground())
	default:
		style = a.theme.Row
	}
	return &style
}

// detailView renders the selected row in full.
func (a *app) detailView() string {
	header := a.headerView()
	tabs := a.tabsView()
	lines := a.detailLines()

	height := a.detailHeight()
	offset := min(a.detailOffset, max(len(lines)-height, 0))
	a.detailOffset = offset
	end := min(offset+height, len(lines))

	body := make([]string, 0, height)
	for _, line := range lines[offset:end] {
		body = append(body, a.theme.Row.Width(a.width).Render(
			ui.Truncate(line, a.width-2)))
	}
	for i := len(body); i < height; i++ {
		body = append(body, a.theme.Row.Width(a.width).Render(""))
	}

	help := ui.HelpBar(a.theme, a.shortHelpKeys(), a.width)
	position := strconv.Itoa(offset+1) + "–" + strconv.Itoa(end) +
		" of " + strconv.Itoa(len(lines)) + " lines  ·  esc to go back"
	status := ui.StatusLine(a.theme, a.statusKind, a.status, position, a.width)
	return strings.Join([]string{header, tabs,
		strings.Join(body, "\n"), help, status}, "\n")
}

// detailLines builds the detail screen's text for whichever row is selected.
func (a *app) detailLines() []string {
	switch a.screen {
	case screenUsers:
		return a.userDetail()
	case screenConnections:
		return a.connDetail()
	case screenServer:
		return a.serverDetail()
	default:
		return a.shareDetail()
	}
}

// shareDetail shows one share in full: what it is, what the Unix side says
// about its directory, who is on it, and the stanza as the server resolved it.
func (a *app) shareDetail() []string {
	share, ok := a.selectedShare()
	if !ok {
		return []string{"(nothing selected)"}
	}
	lines := []string{"[" + share.Name + "]", ""}
	if share.Comment != "" {
		lines = append(lines, "  "+share.Comment, "")
	}
	lines = append(lines,
		"  path          "+orNone(share.Path),
		"  access        "+accessCell(share),
		"  who           "+share.Access(),
		"  write list    "+orNone(strings.Join(share.WriteList, ", ")),
		"  browseable    "+yesNo(share.Browseable),
		"  masks         file "+orNone(share.CreateMask)+", directory "+
			orNone(share.DirectoryMask))
	if len(share.VFSObjects) > 0 {
		lines = append(lines, "  vfs objects   "+strings.Join(share.VFSObjects, ", "))
	}
	if share.Special {
		lines = append(lines, "",
			"  This is one of Samba's own sections rather than a directory",
			"  export, so tui-samba shows it and will not rewrite it.")
	}

	if len(share.Findings) > 0 {
		lines = append(lines, "", "What is worth knowing")
		for _, finding := range share.Findings {
			lines = append(lines, "  "+verdictMark(finding.Verdict)+" "+finding.Message)
		}
	}

	if share.Path != "" {
		lines = append(lines, "", "The directory itself")
		if share.Dir.Exists {
			lines = append(lines,
				"  mode          "+orNone(share.Dir.Mode),
				"  owner         "+orNone(share.Dir.Owner)+":"+
					orNone(share.Dir.Group),
				"  is a directory "+yesNo(share.Dir.IsDir))
			if share.Dir.SELinuxContext != "" {
				lines = append(lines, "  selinux       "+share.Dir.SELinuxContext)
			}
		} else {
			lines = append(lines, "  "+orNone(share.Dir.Note))
		}
		lines = append(lines, "",
			"  Samba can never grant more than the Unix permissions allow, and",
			"  it can never take back what they already give to somebody with a",
			"  shell on this machine.")
	}

	if sessions := a.model.SessionsOn(share.Name); sessions > 0 {
		lines = append(lines, "", "In use right now",
			"  "+plural(sessions, "client")+", "+
				plural(a.model.FilesOn(share.Name), "file")+" open")
		for _, connection := range a.model.TreeConnects {
			if connection.Service == share.Name {
				lines = append(lines, "  "+connection.Machine+"  since "+
					orNone(connection.Since))
			}
		}
	}

	lines = append(lines, "", "Every effective parameter")
	for _, key := range share.ParamKeys() {
		lines = append(lines, "  "+ui.Pad(key, 24)+share.Params[key])
	}
	lines = append(lines, "",
		"  press e to change this share, with a diff to confirm")
	return lines
}

// userDetail shows one account in full.
func (a *app) userDetail() []string {
	user, ok := a.selectedUser()
	if !ok {
		return []string{"(nothing selected)"}
	}
	lines := []string{
		user.Name,
		"",
		"  state         " + userState(user),
		"  flags         " + orNone(user.Flags),
		"  unix uid      " + uidCell(user),
		"  password set  " + orNone(user.PasswordLastSet),
	}
	if user.HomeDirectory != "" {
		lines = append(lines, "  home          "+user.HomeDirectory)
	}
	if user.Note != "" {
		lines = append(lines, "", "  "+user.Note)
	}
	lines = append(lines, "",
		"A Samba account is not a Unix account. This is an entry in Samba's own",
		"password database, and it maps to the Unix account of the same name —",
		"which is what decides the file permissions once somebody is in. Adding",
		"one here does not create a Unix account, and removing one does not",
		"remove it either.",
		"",
		"  press p to set a password, D to disable, x to remove the entry")
	return lines
}

// connDetail shows one session, connection or open file in full.
func (a *app) connDetail() []string {
	row, ok := a.selectedConn()
	if !ok {
		return []string{"(nothing selected)"}
	}
	session := row.session
	lines := []string{
		orNone(session.User) + " from " + orNone(session.Machine),
		"",
		"  pid           " + orNone(session.PID),
		"  unix group    " + orNone(session.Group),
		"  address       " + orNone(session.Remote),
		"  dialect       " + orNone(session.Protocol),
		"  signing       " + orNone(session.Signing),
		"  encryption    " + orNone(session.Encryption),
	}
	if row.kind == rowFile {
		lines = append(lines, "", "The open file",
			"  path          "+row.file.Path,
			"  share         "+orNone(row.file.Service),
			"  access        "+orNone(row.file.Access),
			"  oplock        "+orNone(row.file.Oplock),
			"  byte lock     "+yesNo(row.file.Locked))
	}

	lines = append(lines, "", "Shares this session has open")
	for _, connection := range a.model.TreeConnects {
		if connection.PID != session.PID {
			continue
		}
		lines = append(lines, "  "+ui.Pad(connection.Service, 18)+
			"since "+orNone(connection.Since))
	}

	lines = append(lines, "",
		"This screen is read-only in v0.1. Closing somebody's session is a",
		"thing tui-samba can build a command for and deliberately does not:",
		"a client that is disconnected mid-write loses the write, and no",
		"dialog makes that safe enough to put behind a key.")
	return lines
}

// serverDetail shows one server fact in full.
func (a *app) serverDetail() []string {
	row, ok := a.selectedServer()
	if !ok {
		return []string{"(nothing selected)"}
	}
	lines := []string{row.label, "", "  " + row.value}
	if row.note != "" && row.note != row.value {
		lines = append(lines, "", "  "+row.note)
	}
	lines = append(lines, "", "The server",
		"  version       "+orNone(a.model.Version),
		"  config        "+orNone(a.model.Global.ConfigFile),
		"  hostname      "+orNone(a.model.Hostname))
	lines = append(lines, "", "Every effective global parameter")
	for _, key := range a.model.Global.ParamKeys() {
		lines = append(lines, "  "+ui.Pad(key, 26)+a.model.Global.Params[key])
	}
	return lines
}

// diffForDialog trims a diff to what fits above the command preview, saying
// how much was left out.
func (a *app) diffForDialog(diff string) string {
	budget := max(min(a.height-16, dialogDiffLines), 4)
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	if len(lines) <= budget {
		return diff
	}
	kept := append([]string{}, lines[:budget]...)
	return strings.Join(kept, "\n") + "\n… " +
		strconv.Itoa(len(lines)-budget) + " more diff lines"
}

// orNone renders an empty value as a visible placeholder, so a blank line is
// never mistaken for a missing read.
func orNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// shortHelpKeys is the single-line hint bar, which changes with the screen
// because the keys that do anything change with it.
func (a *app) shortHelpKeys() []ui.KeyHint {
	hints := []ui.KeyHint{{Key: "tab", Desc: "screen"}, {Key: "enter", Desc: "detail"}}
	switch a.screen {
	case screenUsers:
		hints = append(hints,
			ui.KeyHint{Key: "a", Desc: "add"},
			ui.KeyHint{Key: "p", Desc: "password"},
			ui.KeyHint{Key: "E/D", Desc: "enable"})
	case screenConnections:
		hints = append(hints, ui.KeyHint{Key: "R", Desc: "re-read"})
	case screenServer:
		hints = append(hints,
			ui.KeyHint{Key: "r", Desc: "reload"},
			ui.KeyHint{Key: "t", Desc: "self-test"})
	default:
		hints = append(hints,
			ui.KeyHint{Key: "e", Desc: "edit"},
			ui.KeyHint{Key: "n", Desc: "new"})
	}
	return append(hints,
		ui.KeyHint{Key: "/", Desc: "filter"},
		ui.KeyHint{Key: "?", Desc: "help"},
		ui.KeyHint{Key: "q", Desc: "quit"})
}

// helpKeys is the full key list shown on the help screen.
func helpKeys() []ui.KeyHint {
	return []ui.KeyHint{
		{Key: "tab / 1-4", Desc: "shares, accounts, connections, server"},
		{Key: "↑/k, ↓/j", Desc: "move the selection, or scroll the detail screen"},
		{Key: "g / G", Desc: "first / last row"},
		{Key: "pgup/pgdn", Desc: "scroll a page"},
		{Key: "enter", Desc: "open the selected row in full"},
		{Key: "esc", Desc: "leave the detail screen"},
		{Key: "/", Desc: "filter this screen (esc clears)"},
		{Key: "e", Desc: "edit the selected share, with a diff to confirm"},
		{Key: "n", Desc: "add a share"},
		{Key: "a", Desc: "add a Samba account, password read from stdin"},
		{Key: "p", Desc: "set the selected account's password"},
		{Key: "E / D", Desc: "enable / disable the selected account"},
		{Key: "x", Desc: "remove the selected account from the Samba database"},
		{Key: "r", Desc: "tell the running server to re-read its configuration"},
		{Key: "t", Desc: "ask the server what an anonymous client sees"},
		{Key: "R", Desc: "re-read this machine"},
		{Key: "?", Desc: "this help"},
		{Key: "q", Desc: "quit"},
		{Key: "", Desc: ""},
		{Key: "note", Desc: "every change is previewed, and a share is checked by testparm first"},
		{Key: "note", Desc: "no password reaches a command line, no session is closed, nothing restarts"},
	}
}
