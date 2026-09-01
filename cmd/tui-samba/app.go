package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/theme"
	"github.com/tui-tools/tui-kit/ui"
	"github.com/tui-tools/tui-samba/internal/samba"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// screen is one of the four views the tool is made of. They are tabs rather
// than nested screens because they answer four separate questions about the
// same server, and a reader arrives with one of them already in mind.
type screen int

const (
	screenShares screen = iota
	screenUsers
	screenConnections
	screenServer
	screenCount
)

// title names a screen for the tab bar.
func (s screen) title() string {
	switch s {
	case screenUsers:
		return "accounts"
	case screenConnections:
		return "connections"
	case screenServer:
		return "server"
	default:
		return "shares"
	}
}

// mode is the dialog the app currently has open. Only one is open at a time,
// which keeps the update loop flat.
type mode int

const (
	modeBrowse mode = iota
	modeDetail
	modeConfirm
	modeInput
	modePicker
	modeForm
	modeHelp
)

// The prompts a text input is ever opened for.
const (
	promptFilter      = "filter"
	promptAddUser     = "add-user"
	promptPassword    = "password"
	promptDeleteShare = "delete-share"
)

// What a background build is building, so a failed one returns the reader to
// the dialog they came from rather than to whichever was open last.
const (
	buildShare  = "share"
	buildGlobal = "global"
	buildDelete = "delete"
)

// app is the tui-samba Bubble Tea model.
type app struct {
	backend shares.Backend
	theme   theme.Theme
	caps    shares.Capabilities
	// backendCompat is what the version probe found, rendered in the header.
	backendCompat compat.Result

	model shares.Model

	// The rows left after the filter, per screen, in display order.
	shareRows []shares.Share
	userRows  []shares.User
	connRows  []connRow
	serverRow []serverRow

	width, height int
	screen        screen
	// cursor and offset are per screen, so moving between tabs does not lose
	// the row the reader was on.
	cursor [screenCount]int
	offset [screenCount]int
	filter string

	// detailOffset scrolls the detail screen.
	detailOffset int

	mode    mode
	confirm ui.Confirm
	input   ui.Input
	picker  ui.Picker
	form    guidedForm
	// pickerFor names the form field an open picker is filling, and promptFor
	// what an open text prompt is asking about.
	pickerFor string
	promptFor string
	// promptUser is the account a password prompt is for, or the share a typed
	// removal is confirming.
	promptUser string
	// buildFor is what the background build in flight is building, so its
	// failure returns the reader to the dialog they came from.
	buildFor string

	// selfTest is what the server last answered when it was asked for its own
	// share list. It is not part of the model because it is not something the
	// machine has: it is something the user asked for.
	selfTest string

	status     string
	statusKind ui.StatusKind
	loading    bool
	// loadFailed reports that the last Load returned an error, so the empty
	// state does not claim the machine simply has no shares.
	loadFailed bool
	// busy blocks input while a command runs.
	busy bool
}

// loadedMsg carries the result of a Load.
type loadedMsg struct {
	model shares.Model
	err   error
}

// ranMsg carries the result of running a plan.
type ranMsg struct {
	// title is the plan's title, echoed in the status line.
	title  string
	output string
	err    error
	// keep reports that the output is the point of the command rather than a
	// side effect, so it is put on the server screen instead of trimmed to a
	// status line.
	keep bool
}

// builtMsg carries a staged, checked write plan back from the background.
//
// Building one is not instant: it reads the configuration, stages a file and
// waits for Samba's own parser to read it back. That is a command, not a
// keystroke, so it happens off the update loop like every other command.
type builtMsg struct {
	plan shares.WritePlan
	err  error
}

// plan is what a confirm dialog is holding: one or more commands, run in
// order. Reloading is a single command; writing a share is up to four, and all
// of them are shown before any runs.
type plan struct {
	title    string
	commands []shares.Command
	// showOutput marks a command run for what it prints rather than for what
	// it changes. The self-test is the only one: its answer is the whole
	// point, so it is kept and shown rather than summarised away.
	showOutput bool
}

// newApp builds the model around a backend.
func newApp(backend shares.Backend, th theme.Theme,
	backendCompat compat.Result) *app {
	a := &app{
		backend:       backend,
		theme:         th,
		caps:          backend.Capabilities(),
		backendCompat: backendCompat,
		width:         80,
		height:        24,
		loading:       true,
	}
	if th.Warning != "" {
		a.setStatus(ui.StatusWarn, th.Warning)
	}
	return a
}

// Init starts the first load.
func (a *app) Init() tea.Cmd { return a.load() }

// loadTimeout bounds a read. Running testparm, pdbedit and smbstatus and
// stat'ing a handful of directories is fast; a machine whose share path is on
// a network file system that has gone away must not hang the tool forever.
const loadTimeout = 60 * time.Second

// load reads the file server in the background.
func (a *app) load() tea.Cmd {
	backend := a.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		model, err := backend.Load(ctx)
		return loadedMsg{model: model, err: err}
	}
}

// run executes a confirmed plan in the background, one command at a time,
// stopping at the first failure.
func (a *app) run(p plan) tea.Cmd {
	backend := a.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		var outputs []string
		for _, cmd := range p.commands {
			out, err := backend.Run(ctx, cmd)
			if err != nil {
				return ranMsg{title: p.title, output: out, err: err}
			}
			if trimmed := strings.TrimSpace(out); trimmed != "" {
				outputs = append(outputs, trimmed)
			}
		}
		return ranMsg{title: p.title, keep: p.showOutput,
			output: strings.Join(outputs, "\n")}
	}
}

// build stages the share the form describes and has Samba check it, in the
// background.
func (a *app) build(req shares.ShareRequest) tea.Cmd {
	backend, model := a.backend, a.model
	a.buildFor = buildShare
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		built, err := backend.BuildShareWrite(ctx, model, req)
		return builtMsg{plan: built, err: err}
	}
}

// buildGlobalWrite stages the server-wide settings the same way.
func (a *app) buildGlobalWrite(req shares.GlobalRequest) tea.Cmd {
	backend, model := a.backend, a.model
	a.buildFor = buildGlobal
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		built, err := backend.BuildGlobalWrite(ctx, model, req)
		return builtMsg{plan: built, err: err}
	}
}

// buildShareDelete works out what removing a share would take, which means
// reading the configuration to find out whether it is this tool's to remove at
// all.
func (a *app) buildShareDelete(name string) tea.Cmd {
	backend, model := a.backend, a.model
	a.buildFor = buildDelete
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		built, err := backend.BuildShareDelete(ctx, model, name)
		return builtMsg{plan: built, err: err}
	}
}

// setStatus records a plain message for the status line.
func (a *app) setStatus(kind ui.StatusKind, message string) {
	a.status = message
	a.statusKind = kind
}

// setStatusf records a formatted message for the status line.
func (a *app) setStatusf(kind ui.StatusKind, format string, args ...any) {
	a.setStatus(kind, fmt.Sprintf(format, args...))
}

// Update is the main event loop.
func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.clampCursor()
		return a, nil

	case loadedMsg:
		a.loading = false
		if msg.err != nil {
			a.loadFailed = true
			a.setStatus(ui.StatusError, msg.err.Error())
			return a, nil
		}
		a.loadFailed = false
		a.model = msg.model
		a.applyFilter()
		return a, nil

	case builtMsg:
		a.busy = false
		if msg.err != nil {
			a.setStatus(ui.StatusError, msg.err.Error())
			// A form can be corrected; a removal has nothing to go back to.
			if a.buildFor == buildDelete {
				a.mode = modeBrowse
			} else {
				a.mode = modeForm
			}
			return a, nil
		}
		a.openWrite(msg.plan)
		return a, nil

	case ranMsg:
		a.busy = false
		if msg.err != nil {
			a.setStatus(ui.StatusError, msg.err.Error())
			return a, a.load()
		}
		summary := strings.TrimSpace(msg.output)
		if msg.keep {
			// The self-test's answer is what was asked for, so it is kept and
			// shown on the server screen rather than trimmed to one line.
			a.selfTest = summary
			a.screen = screenServer
			a.setStatusf(ui.StatusOK, "%s answered — see the server screen",
				msg.title)
			a.applyFilter()
			return a, nil
		}
		if summary == "" {
			summary = "done"
		}
		a.setStatusf(ui.StatusOK, "%s: %s", msg.title, firstLine(summary))
		a.loading = true
		return a, a.load()

	case tea.KeyMsg:
		return a.handleKey(msg)
	}

	// Anything else (cursor blink, …) only concerns an open text input.
	if a.mode == modeInput {
		cmd, _ := a.input.Update(msg)
		return a, cmd
	}
	if a.mode == modeForm {
		return a, a.form.updateActive(msg)
	}
	return a, nil
}

// handleKey routes a key press to the open dialog, or to the current screen.
func (a *app) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits, even mid-dialog.
	if msg.Type == tea.KeyCtrlC {
		return a, tea.Quit
	}
	if a.busy {
		// A command is running: swallow input rather than queueing surprises.
		return a, nil
	}

	switch a.mode {
	case modeConfirm:
		return a.handleConfirm(msg)
	case modeInput:
		return a.handleInput(msg)
	case modePicker:
		return a.handlePicker(msg)
	case modeForm:
		return a.handleForm(msg)
	case modeHelp:
		a.mode = modeBrowse
		return a, nil
	case modeDetail:
		return a.handleDetailKey(msg)
	default:
		return a.handleBrowseKey(msg)
	}
}

// handleConfirm resolves the confirm dialog.
func (a *app) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a.confirm.Update(msg)
	if !a.confirm.Done {
		return a, nil
	}
	a.mode = modeBrowse
	confirmed := a.confirm.Confirmed
	pending, ok := a.confirm.Payload.(plan)
	a.confirm = ui.Confirm{}
	if !confirmed || !ok {
		a.setStatus(ui.StatusInfo, "cancelled")
		return a, nil
	}
	a.busy = true
	a.setStatusf(ui.StatusInfo, "running %s…", a.backend.Preview(pending.commands[0]))
	return a, a.run(pending)
}

// handleInput resolves the text prompt, which serves the filter, adding an
// account and setting a password.
func (a *app) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd, _ := a.input.Update(msg)
	if !a.input.Done {
		if a.promptFor == promptFilter {
			// Filter as the user types.
			a.filter = a.input.Value()
			a.applyFilter()
		}
		return a, cmd
	}
	accepted, value := a.input.Accepted, strings.TrimSpace(a.input.Value())
	prompt, account := a.promptFor, a.promptUser
	a.mode, a.promptFor, a.promptUser = modeBrowse, "", ""
	a.input = ui.Input{}

	switch prompt {
	case promptAddUser:
		if !accepted || value == "" {
			a.setStatus(ui.StatusInfo, "cancelled")
			return a, nil
		}
		return a, a.askPassword(value, true)
	case promptPassword:
		if !accepted || value == "" {
			a.setStatus(ui.StatusInfo, "cancelled")
			return a, nil
		}
		return a, a.confirmPassword(account, value)
	case promptDeleteShare:
		if !accepted {
			a.setStatus(ui.StatusInfo, "cancelled")
			return a, nil
		}
		if value != account {
			a.setStatusf(ui.StatusWarn,
				"that is not [%s], so nothing was removed", account)
			return a, nil
		}
		a.busy = true
		a.setStatusf(ui.StatusInfo,
			"working out what removing [%s] would take…", account)
		return a, a.buildShareDelete(account)
	}

	if accepted {
		a.filter = value
	} else {
		a.filter = ""
	}
	a.applyFilter()
	return a, nil
}

// handlePicker resolves the open picker, which serves the share form's choice
// fields.
func (a *app) handlePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a.picker.Update(msg)
	if !a.picker.Done {
		return a, nil
	}
	choice, accepted := a.picker.Selected(), a.picker.Accepted
	field := a.pickerFor
	a.picker, a.pickerFor = ui.Picker{}, ""
	if accepted {
		a.form.set(field, choice)
	}
	a.mode = modeForm
	return a, nil
}

// handleForm routes keys to the share form.
func (a *app) handleForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.mode = modeBrowse
		a.setStatus(ui.StatusInfo, "cancelled")
		return a, nil
	case "tab", "down":
		a.form.next()
		return a, nil
	case "shift+tab", "up":
		a.form.prev()
		return a, nil
	case "left":
		if a.form.activeIsChoice() {
			a.form.cycle(-1)
			return a, nil
		}
	case "right":
		if a.form.activeIsChoice() {
			a.form.cycle(1)
			return a, nil
		}
	case " ":
		// Space opens the list for a choice field. It is not enter, because
		// enter has to mean "review the change" from every field, and a form
		// whose first field is a choice would otherwise be a dead end.
		if a.form.activeIsChoice() {
			a.pickerFor = a.form.activeKey()
			a.picker = ui.NewPicker(a.form.activeLabel(),
				a.form.activeOptions(), a.form.activeValue())
			a.mode = modePicker
			return a, nil
		}
	case "enter":
		return a, a.submitForm()
	}
	return a, a.form.updateActive(msg)
}

// submitForm hands the form to the backend, which stages the file and has
// Samba read it back before anything is confirmed.
func (a *app) submitForm() tea.Cmd {
	if a.form.kind == formGlobal {
		a.busy = true
		a.setStatus(ui.StatusInfo,
			"staging the configuration and asking testparm to read it…")
		return a.buildGlobalWrite(a.form.globalRequest())
	}
	request, err := a.form.request()
	if err != nil {
		a.setStatus(ui.StatusError, err.Error())
		return nil
	}
	a.busy = true
	a.setStatus(ui.StatusInfo,
		"staging the configuration and asking testparm to read it…")
	return a.build(request)
}

// openWrite shows a staged, checked change: what Samba said about it, the
// diff, and the commands that apply it.
func (a *app) openWrite(write shares.WritePlan) {
	a.mode = modeConfirm
	a.confirm = ui.Confirm{
		Title:   write.Title,
		Body:    a.writeBody(write),
		Command: a.previewAll(write.Commands),
		Danger:  true,
		Payload: plan{title: write.Title, commands: write.Commands},
	}
}

// writeBody is what the confirm dialog says above the commands: whether Samba
// accepted the staged file, the caveat that applies, who is connected to the
// share right now, and the diff.
func (a *app) writeBody(write shares.WritePlan) string {
	var parts []string
	switch {
	case write.Validated:
		parts = append(parts, "✓ "+write.Validation)
	case write.Validation != "":
		parts = append(parts, "! the staged file "+write.Validation)
	}
	if write.ValidationCommand != "" {
		parts = append(parts, "checked with: "+write.ValidationCommand)
	}
	if write.Warning != "" {
		parts = append(parts, write.Warning)
	}
	if inUse := a.inUseNote(write); inUse != "" {
		parts = append(parts, inUse)
	}
	parts = append(parts, a.diffForDialog(write.Diff))
	return strings.Join(parts, "\n\n")
}

// inUseNote says who is on the share being changed, which is the fact a reader
// wants before they answer yes.
//
// A reload does not disconnect anybody, and that is the reassuring half; the
// other half is that a client already inside a share keeps the access it was
// given when it connected, so tightening a share does not take effect for the
// people who are in it until they come back.
func (a *app) inUseNote(write shares.WritePlan) string {
	name := shareNameOf(write.Title)
	if name == "" {
		return ""
	}
	sessions := a.model.SessionsOn(name)
	files := a.model.FilesOn(name)
	if sessions == 0 && files == 0 {
		return ""
	}
	return "Right now " + plural(sessions, "client") + " " +
		verb(sessions) + " [" + name + "] open, with " +
		plural(files, "file") + " in use. Reloading disconnects nobody, and a " +
		"client already inside keeps the access it was granted until it " +
		"reconnects."
}

// shareNameOf pulls the share name out of a plan title, which is where the
// backend put it.
func shareNameOf(title string) string {
	open := strings.Index(title, "[")
	closing := strings.Index(title, "]")
	if open < 0 || closing < open {
		return ""
	}
	return title[open+1 : closing]
}

// plural renders a count with its noun.
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}

// verb agrees with the count in front of it.
func verb(count int) string {
	if count == 1 {
		return "has"
	}
	return "have"
}

// previewAll renders every command of a plan, one per line, each with the
// prompt the dialog puts in front of the first one.
func (a *app) previewAll(commands []shares.Command) string {
	previews := make([]string, 0, len(commands))
	for _, cmd := range commands {
		previews = append(previews, a.backend.Preview(cmd))
	}
	return strings.Join(previews, "\n$ ")
}

// handleBrowseKey handles a screen's own keys.
func (a *app) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return a, tea.Quit
	case "?":
		a.mode = modeHelp
	case "j", "down":
		a.moveCursor(1)
	case "k", "up":
		a.moveCursor(-1)
	case "g", "home":
		a.cursor[a.screen], a.offset[a.screen] = 0, 0
	case "G", "end":
		a.cursor[a.screen] = max(a.rowCount()-1, 0)
		a.clampCursor()
	case "pgdown", "ctrl+f":
		a.moveCursor(a.tableHeight())
	case "pgup", "ctrl+b":
		a.moveCursor(-a.tableHeight())
	case "tab", "l", "right":
		a.gotoScreen((a.screen + 1) % screenCount)
	case "shift+tab", "h", "left":
		a.gotoScreen((a.screen + screenCount - 1) % screenCount)
	case "1", "2", "3", "4":
		a.gotoScreen(screen(msg.String()[0] - '1'))
	case "/":
		a.input = ui.NewInput("Filter "+a.screen.title(), "any column…", a.filter)
		a.input.Help = "Matches any column of this screen. Empty clears the filter."
		a.promptFor, a.mode = promptFilter, modeInput
	case "enter":
		if a.rowCount() == 0 {
			a.setStatus(ui.StatusWarn, "nothing selected")
			return a, nil
		}
		a.mode, a.detailOffset = modeDetail, 0
	case "R", "ctrl+r":
		a.loading = true
		return a, a.load()
	default:
		return a, a.handleActionKey(msg)
	}
	return a, nil
}

// handleDetailKey handles the per-row screen. The action keys are the same
// ones the table offers, applied to the row on screen.
func (a *app) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "backspace", "left":
		a.mode, a.detailOffset = modeBrowse, 0
		return a, nil
	case "?":
		a.mode = modeHelp
		return a, nil
	case "j", "down":
		a.detailOffset++
		return a, nil
	case "k", "up":
		a.detailOffset = max(a.detailOffset-1, 0)
		return a, nil
	case "g", "home":
		a.detailOffset = 0
		return a, nil
	case "pgdown", "ctrl+f":
		a.detailOffset += a.detailHeight()
		return a, nil
	case "pgup", "ctrl+b":
		a.detailOffset = max(a.detailOffset-a.detailHeight(), 0)
		return a, nil
	case "R", "ctrl+r":
		a.loading = true
		return a, a.load()
	default:
		return a, a.handleActionKey(msg)
	}
}

// handleActionKey handles the keys that mean the same thing on every screen.
func (a *app) handleActionKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "e":
		return a.openShareForm(false)
	case "n":
		return a.openShareForm(true)
	case "X":
		return a.askDeleteShare()
	case "o":
		return a.openGlobalForm()
	case "a":
		return a.openAddUser()
	case "p":
		return a.openPassword()
	case "x":
		return a.confirmUserAction(shares.UserDelete)
	case "E":
		return a.confirmUserAction(shares.UserEnable)
	case "D":
		return a.confirmUserAction(shares.UserDisable)
	case "r":
		return a.confirmReload()
	case "t":
		return a.confirmSelfTest()
	}
	return nil
}

// openShareForm opens the guided editor, on the selected share or on a new
// one.
func (a *app) openShareForm(create bool) tea.Cmd {
	if !a.caps.CanEditShares {
		reason := a.caps.EditReason
		if reason == "" {
			reason = "this backend cannot write a share"
		}
		a.setStatus(ui.StatusWarn, reason)
		return nil
	}
	if create {
		a.form = newShareForm(shares.Share{}, true)
		a.mode = modeForm
		return nil
	}
	share, ok := a.selectedShare()
	if !ok {
		a.setStatus(ui.StatusWarn,
			"no share selected — press 1 for the shares screen, or n for a new one")
		return nil
	}
	if err := samba.CheckShareName(share.Name); err != nil {
		a.setStatus(ui.StatusWarn, err.Error())
		return nil
	}
	a.form = newShareForm(share, false)
	a.mode = modeForm
	return nil
}

// openGlobalForm opens the server-wide editor, seeded from what the server
// itself resolved.
func (a *app) openGlobalForm() tea.Cmd {
	if !a.caps.CanEditShares {
		reason := a.caps.EditReason
		if reason == "" {
			reason = "this backend cannot write a configuration"
		}
		a.setStatus(ui.StatusWarn, reason)
		return nil
	}
	a.form = newGlobalForm(a.model.Global)
	a.mode = modeForm
	return nil
}

// askDeleteShare asks for the share's name to be typed back before anything is
// built.
//
// Typing it is the deliberate step. A removal is the one change here that
// cannot be undone by making the opposite change — the file is gone, and with
// it every parameter of the share that this form never asked about — so it
// takes a keystroke that cannot be the wrong row under a cursor.
func (a *app) askDeleteShare() tea.Cmd {
	if !a.caps.CanEditShares {
		reason := a.caps.EditReason
		if reason == "" {
			reason = "this backend cannot write a share"
		}
		a.setStatus(ui.StatusWarn, reason)
		return nil
	}
	share, ok := a.selectedShare()
	if !ok {
		a.setStatus(ui.StatusWarn,
			"no share selected — press 1 for the shares screen")
		return nil
	}
	if err := samba.CheckShareName(share.Name); err != nil {
		a.setStatus(ui.StatusWarn, err.Error())
		return nil
	}
	a.input = ui.NewInput("Remove the share ["+share.Name+"]",
		"type "+share.Name+" to confirm…", "")
	a.input.Help = "Only a share tui-samba wrote can be removed here: its own " +
		"drop-in file goes, and so does the one `include` line that reached it. " +
		"The exported directory and everything in it are left alone. The exact " +
		"commands are shown before any of them runs."
	a.promptFor, a.promptUser, a.mode = promptDeleteShare, share.Name, modeInput
	return nil
}

// openAddUser asks for the account name, and then for its password.
func (a *app) openAddUser() tea.Cmd {
	if !a.caps.CanManageUsers {
		a.setStatus(ui.StatusWarn, a.usersReason())
		return nil
	}
	a.input = ui.NewInput("Add a Samba account", "an existing Unix account…", "")
	a.input.Help = "Samba maps every account to a Unix one, which has to exist " +
		"already. The password is asked for next, and never appears in a " +
		"command line."
	a.promptFor, a.mode = promptAddUser, modeInput
	return nil
}

// openPassword asks for a new password for the selected account.
func (a *app) openPassword() tea.Cmd {
	if !a.caps.CanManageUsers {
		a.setStatus(ui.StatusWarn, a.usersReason())
		return nil
	}
	user, ok := a.selectedUser()
	if !ok {
		a.setStatus(ui.StatusWarn,
			"no account selected — press 2 for the accounts screen")
		return nil
	}
	return a.askPassword(user.Name, false)
}

// askPassword opens the masked prompt for one account.
func (a *app) askPassword(name string, adding bool) tea.Cmd {
	if err := samba.CheckUser(name); err != nil {
		a.setStatus(ui.StatusError, err.Error())
		return nil
	}
	title := "Set " + name + "'s Samba password"
	if adding {
		title = "Password for the new account " + name
	}
	a.input = ui.NewInput(title, "the password…", "")
	a.input.Model.EchoMode = maskedEcho
	a.input.Help = "It is written to smbpasswd's standard input, so it never " +
		"appears in a command line, in `ps`, or in the preview below."
	a.promptFor, a.promptUser, a.mode = promptPassword, name, modeInput
	return nil
}

// confirmPassword builds the account command and opens the confirm dialog.
//
// The password is in the command's standard input and in nothing else, which
// is why the dialog can show the command line in full: there is nothing on it
// worth hiding.
func (a *app) confirmPassword(name, password string) tea.Cmd {
	_, exists := a.model.User(name)
	var cmd shares.Command
	var err error
	if exists {
		cmd, err = a.backend.BuildSetPassword(a.model, name, password)
	} else {
		cmd, err = a.backend.BuildUserAdd(a.model, name, password)
	}
	if err != nil {
		a.setStatus(ui.StatusError, err.Error())
		return nil
	}
	title := cmd.Description
	a.openConfirm(title,
		"The password is written to the command's standard input and appears "+
			"nowhere else — not in the command line above, not in `ps`, and not "+
			"in this dialog.", cmd)
	return nil
}

// confirmUserAction asks before changing an account.
func (a *app) confirmUserAction(action string) tea.Cmd {
	if !a.caps.CanManageUsers {
		a.setStatus(ui.StatusWarn, a.usersReason())
		return nil
	}
	user, ok := a.selectedUser()
	if !ok {
		a.setStatus(ui.StatusWarn,
			"no account selected — press 2 for the accounts screen")
		return nil
	}
	cmd, err := a.backend.BuildUserAction(a.model, user.Name, action)
	if err != nil {
		a.setStatus(ui.StatusError, err.Error())
		return nil
	}
	body := cmd.Description + "."
	if action == shares.UserDelete {
		body += "\n\nThe Unix account of the same name is left exactly as it " +
			"is: this removes the Samba entry only, and the person can still " +
			"log in over SSH or at the console. Disabling with D keeps the " +
			"entry and its password, which is what to do when somebody may " +
			"come back."
	}
	a.openConfirm(cmd.Description, body, cmd)
	return nil
}

// usersReason is why the account keys do nothing, in the words the status line
// shows.
func (a *app) usersReason() string {
	if a.caps.UsersReason != "" {
		return a.caps.UsersReason
	}
	return "this backend cannot change the Samba password database"
}

// confirmReload asks before telling the server to re-read its configuration.
func (a *app) confirmReload() tea.Cmd {
	cmd, err := a.backend.BuildReload(a.model)
	if err != nil {
		a.setStatus(ui.StatusWarn, err.Error())
		return nil
	}
	a.openConfirm("Reload the configuration",
		cmd.Description+".\n\nThis is a reload and not a restart: every smbd "+
			"re-reads its files and keeps serving. A client that is copying a "+
			"file does not notice. Restarting the service would drop all of "+
			"them, and tui-samba never does that.", cmd)
	return nil
}

// confirmSelfTest asks before connecting to the server as a client.
func (a *app) confirmSelfTest() tea.Cmd {
	cmd, err := a.backend.BuildSelfTest(a.model)
	if err != nil {
		a.setStatus(ui.StatusWarn, err.Error())
		return nil
	}
	a.mode = modeConfirm
	a.confirm = ui.Confirm{
		Title: "Ask the server what a client sees",
		Body: cmd.Description + ".\n\nIt reads and changes nothing. What it " +
			"answers is the share list an anonymous client on the network gets, " +
			"which is a different question from what the configuration says — a " +
			"share that is not browseable is real and will not be in it.",
		Command: a.backend.Preview(cmd),
		Payload: plan{title: "smbclient -L", commands: []shares.Command{cmd},
			showOutput: true},
	}
	return nil
}

// openConfirm shows one command and what it does.
func (a *app) openConfirm(title, body string, cmd shares.Command) {
	a.mode = modeConfirm
	a.confirm = ui.Confirm{
		Title:   title,
		Body:    body,
		Command: a.backend.Preview(cmd),
		Danger:  cmd.Destructive,
		Payload: plan{title: title, commands: []shares.Command{cmd}},
	}
}

// gotoScreen switches tabs, keeping the filter applied.
func (a *app) gotoScreen(next screen) {
	if next < 0 || next >= screenCount {
		return
	}
	a.screen = next
	a.clampCursor()
}

// connRow is one line of the connections screen: a session, a share it has
// open, or a file open under one.
type connRow struct {
	kind string
	// who, what and where are the three columns; detail is the rest.
	who    string
	what   string
	where  string
	detail string
	// session and file carry the row's own record for the detail screen.
	session shares.Session
	file    shares.OpenFile
}

// The kinds of row the connections screen carries.
const (
	rowSession = "session"
	rowShare   = "share"
	rowFile    = "file"
)

// serverRow is one line of the server screen: a unit, a port, a global
// setting, or one of the server-wide findings.
type serverRow struct {
	label string
	value string
	note  string
	// warn paints the row.
	warn bool
	bad  bool
}

// applyFilter recomputes every screen's visible rows from the current filter.
func (a *app) applyFilter() {
	needle := strings.ToLower(a.filter)
	keep := func(haystack string) bool {
		return needle == "" || strings.Contains(strings.ToLower(haystack), needle)
	}

	a.shareRows = nil
	for _, share := range a.model.Shares {
		if keep(share.Haystack()) {
			a.shareRows = append(a.shareRows, share)
		}
	}
	a.userRows = nil
	for _, user := range a.model.Users {
		if keep(user.Haystack()) {
			a.userRows = append(a.userRows, user)
		}
	}
	a.connRows = nil
	for _, row := range a.allConnRows() {
		if keep(row.who + " " + row.what + " " + row.where + " " + row.detail) {
			a.connRows = append(a.connRows, row)
		}
	}
	a.serverRow = nil
	for _, row := range a.allServerRows() {
		if keep(row.label + " " + row.value + " " + row.note) {
			a.serverRow = append(a.serverRow, row)
		}
	}
	a.clampCursor()
}

// allConnRows flattens the sessions, the shares they have open and the files
// under them into one list.
//
// One list rather than three screens, because the question a reader arrives
// with is "who is on this server and what are they doing", and the answer to
// it is a session with its shares and its files under it.
func (a *app) allConnRows() []connRow {
	var rows []connRow
	for _, session := range a.model.Sessions {
		rows = append(rows, connRow{
			kind: rowSession, who: session.User, what: session.Machine,
			where: session.Protocol, session: session,
			detail: "signing " + orNone(session.Signing) + ", encryption " +
				orNone(session.Encryption),
		})
		for _, connection := range a.model.TreeConnects {
			if connection.PID != session.PID {
				continue
			}
			rows = append(rows, connRow{
				kind: rowShare, who: "  └ " + connection.Service,
				what: connection.Machine, where: "connected",
				detail: connection.Since, session: session,
			})
			for _, file := range a.model.OpenFiles {
				if file.PID != session.PID || file.Service != connection.Service {
					continue
				}
				state := "open"
				if file.Locked {
					state = "locked"
				}
				rows = append(rows, connRow{
					kind: rowFile, who: "    · " + file.Path,
					what: orNone(file.Access), where: state,
					detail: "oplock " + orNone(file.Oplock),
					file:   file, session: session,
				})
			}
		}
	}
	return rows
}

// allServerRows flattens the server itself into rows: what is running, what is
// listening, how it authenticates, and what is wrong with any of that.
func (a *app) allServerRows() []serverRow {
	var rows []serverRow
	for _, finding := range a.model.Global.Findings {
		rows = append(rows, serverRow{
			label: string(finding.Verdict), value: finding.Message,
			warn: true, bad: finding.Verdict == shares.VerdictRisk,
		})
	}
	for _, service := range a.model.Services {
		value := service.Detail
		if service.Present {
			value = service.Unit + "  —  " + orNone(service.State) + ", " +
				orNone(service.Enablement) + " at boot"
		}
		rows = append(rows, serverRow{
			label: service.Role, value: value, note: service.Detail,
			warn: service.Role == shares.RoleFileServer && !service.Active,
			bad:  service.Role == shares.RoleFileServer && !service.Present,
		})
	}
	if len(a.model.Ports) == 0 {
		rows = append(rows, serverRow{label: "listening",
			value: orNone(a.model.PortsDetail), warn: true,
			note: "nothing is listening on 445 or 139, so no client can reach " +
				"this server whatever the configuration says"})
	}
	for _, port := range a.model.Ports {
		rows = append(rows, serverRow{label: "listening",
			value: port.Address + ":" + strconv.Itoa(port.Port) + "  " +
				orNone(port.Process)})
	}
	global := a.model.Global
	rows = append(rows,
		serverRow{label: "config", value: orNone(global.ConfigFile)},
		serverRow{label: "workgroup", value: orNone(global.Workgroup)},
		serverRow{label: "server string", value: orNone(global.ServerString)},
		serverRow{label: "security", value: orNone(global.Security)},
		serverRow{label: "map to guest", value: orNone(global.MapToGuest)},
		serverRow{label: "protocols",
			value: orNone(global.MinProtocol) + " to " + orNone(global.MaxProtocol)},
	)
	for _, include := range global.Includes {
		rows = append(rows, serverRow{label: "include", value: include})
	}
	if a.model.SELinux.Enabled {
		rows = append(rows, serverRow{label: "selinux",
			value: orNone(a.model.SELinux.Mode), note: a.model.SELinux.Detail})
		for _, name := range sortedBooleans(a.model.SELinux.Booleans) {
			value := "off"
			if a.model.SELinux.Booleans[name] {
				value = "on"
			}
			rows = append(rows, serverRow{label: name, value: value})
		}
	}
	if a.model.UsersDetail != "" {
		rows = append(rows, serverRow{label: "accounts",
			value: a.model.UsersDetail, warn: true})
	}
	if a.model.StatusDetail != "" {
		rows = append(rows, serverRow{label: "connections",
			value: a.model.StatusDetail, warn: true})
	}
	if a.model.ConfigDetail != "" {
		rows = append(rows, serverRow{label: "configuration",
			value: a.model.ConfigDetail, warn: true, bad: true})
	}
	for i, line := range splitLines(a.selfTest) {
		label := ""
		if i == 0 {
			label = "smbclient -L"
		}
		rows = append(rows, serverRow{label: label, value: line,
			note: "what an anonymous client on the network is shown"})
	}
	return rows
}

// splitLines splits a command's output into rows, dropping the blank ones a
// share listing is padded with.
func splitLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimRight(line, " \t"))
		}
	}
	return out
}

// sortedBooleans keeps the SELinux switches in a stable order.
func sortedBooleans(values map[string]bool) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

// rowCount is how many rows the current screen has after the filter.
func (a *app) rowCount() int {
	switch a.screen {
	case screenUsers:
		return len(a.userRows)
	case screenConnections:
		return len(a.connRows)
	case screenServer:
		return len(a.serverRow)
	default:
		return len(a.shareRows)
	}
}

// selectedShare is the highlighted row of the shares screen.
func (a *app) selectedShare() (shares.Share, bool) {
	if a.screen != screenShares {
		return shares.Share{}, false
	}
	index := a.cursor[screenShares]
	if index < 0 || index >= len(a.shareRows) {
		return shares.Share{}, false
	}
	return a.shareRows[index], true
}

// selectedUser is the highlighted row of the accounts screen.
func (a *app) selectedUser() (shares.User, bool) {
	if a.screen != screenUsers {
		return shares.User{}, false
	}
	index := a.cursor[screenUsers]
	if index < 0 || index >= len(a.userRows) {
		return shares.User{}, false
	}
	return a.userRows[index], true
}

// selectedConn is the highlighted row of the connections screen.
func (a *app) selectedConn() (connRow, bool) {
	if a.screen != screenConnections {
		return connRow{}, false
	}
	index := a.cursor[screenConnections]
	if index < 0 || index >= len(a.connRows) {
		return connRow{}, false
	}
	return a.connRows[index], true
}

// selectedServer is the highlighted row of the server screen.
func (a *app) selectedServer() (serverRow, bool) {
	if a.screen != screenServer {
		return serverRow{}, false
	}
	index := a.cursor[screenServer]
	if index < 0 || index >= len(a.serverRow) {
		return serverRow{}, false
	}
	return a.serverRow[index], true
}

// moveCursor moves the selection and keeps the viewport in sync.
func (a *app) moveCursor(delta int) {
	a.cursor[a.screen] += delta
	a.clampCursor()
}

// clampCursor keeps the cursor and the scroll offset of every screen in range.
func (a *app) clampCursor() {
	for s := screen(0); s < screenCount; s++ {
		count := a.countFor(s)
		if count == 0 {
			a.cursor[s], a.offset[s] = 0, 0
			continue
		}
		a.cursor[s] = min(max(a.cursor[s], 0), count-1)

		height := a.tableHeight()
		if a.cursor[s] < a.offset[s] {
			a.offset[s] = a.cursor[s]
		}
		if a.cursor[s] >= a.offset[s]+height {
			a.offset[s] = a.cursor[s] - height + 1
		}
		a.offset[s] = max(min(a.offset[s], max(count-height, 0)), 0)
	}
}

// countFor is rowCount for a screen that is not the current one.
func (a *app) countFor(s screen) int {
	current := a.screen
	a.screen = s
	count := a.rowCount()
	a.screen = current
	return count
}

// firstLine keeps status messages to one line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
