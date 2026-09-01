package main

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-kit/theme"
	"github.com/tui-tools/tui-samba/internal/samba"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// newTestApp builds an app on the sample server, sized like a normal terminal
// and already loaded.
func newTestApp(t *testing.T) (*app, *samba.Fake) {
	t.Helper()
	backend := samba.NewFake()
	a := newApp(backend, theme.New(), compat.Result{})
	a.width, a.height = 110, 30
	drain(t, a, a.Init())
	return a, backend
}

// drain runs a tea.Cmd and feeds its message back into the model, which is
// what the Bubble Tea runtime does. It is how a test exercises a load.
func drain(t *testing.T, a *app, cmd tea.Cmd) {
	t.Helper()
	for range 6 {
		if cmd == nil {
			return
		}
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = a.Update(msg)
	}
}

// press sends one key and returns the command it produced.
func press(a *app, key string) tea.Cmd {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	_, cmd := a.Update(msg)
	return cmd
}

// typeIn sends a whole string, one key at a time, into an open prompt.
//
// The command each keystroke returns is deliberately dropped rather than
// drained: it is the text input's cursor blink, and running it would make the
// test wait out a real timer for every character typed.
func typeIn(t *testing.T, a *app, text string) {
	t.Helper()
	for _, r := range text {
		_ = press(a, string(r))
	}
}

// setField fills one form field the way typing into it would, so the value
// survives the save the next keystroke performs.
func setField(f *guidedForm, key, value string) {
	f.values[key] = value
	f.focusActive()
}

// gotoScreen moves to a tab by its number key.
func gotoScreen(t *testing.T, a *app, s screen) {
	t.Helper()
	drain(t, a, press(a, strconv.Itoa(int(s)+1)))
	if a.screen != s {
		t.Fatalf("did not reach the %s screen", s.title())
	}
}

// selectShare moves the cursor to a share by name.
func selectShare(t *testing.T, a *app, name string) shares.Share {
	t.Helper()
	gotoScreen(t, a, screenShares)
	for i, share := range a.shareRows {
		if share.Name == name {
			a.cursor[screenShares] = i
			return share
		}
	}
	t.Fatalf("no share called %q on the sample server", name)
	return shares.Share{}
}

// selectUser moves the cursor to an account by name.
func selectUser(t *testing.T, a *app, name string) {
	t.Helper()
	gotoScreen(t, a, screenUsers)
	for i, user := range a.userRows {
		if user.Name == name {
			a.cursor[screenUsers] = i
			return
		}
	}
	t.Fatalf("no account called %q on the sample server", name)
}

func TestLoadsTheSampleServer(t *testing.T) {
	a, _ := newTestApp(t)
	if len(a.shareRows) != 4 {
		t.Fatalf("loaded %d shares, want the sample server's 4", len(a.shareRows))
	}
	counts := a.model.Count()
	if counts.Guest != 1 || counts.PathMissing != 1 || counts.Users != 3 {
		t.Errorf("counts = %+v", counts)
	}

	// Findings first: what is handing out too much is at the top, not whatever
	// sorted alphabetically.
	if a.shareRows[0].Verdict != shares.VerdictRisk {
		t.Errorf("the first row is %q, want the worst one", a.shareRows[0].Verdict)
	}
	if a.shareRows[len(a.shareRows)-1].Verdict != shares.VerdictOK {
		t.Errorf("the last row is %q", a.shareRows[len(a.shareRows)-1].Verdict)
	}

	view := a.View()
	if !strings.Contains(view, "team") || !strings.Contains(view, "shares") {
		t.Errorf("the first frame is missing the inventory")
	}
}

// TestTheSampleServerHasTheStatesARealOneIsFoundIn: the demo exists to show
// them, and a demo that quietly lost one would no longer demonstrate anything.
func TestTheSampleServerHasTheStatesARealOneIsFoundIn(t *testing.T) {
	a, _ := newTestApp(t)
	byName := map[string]shares.Share{}
	for _, share := range a.shareRows {
		byName[share.Name] = share
	}

	checks := []struct {
		name string
		want func(shares.Share) bool
		why  string
	}{
		{"public", func(s shares.Share) bool {
			return s.GuestOK && s.ReadOnly && s.Dir.Exists
		}, "the read-only share anybody can open"},
		{"homes", func(s shares.Share) bool {
			return s.Special && !s.Browseable
		}, "the home directories every distribution ships"},
		{"team", func(s shares.Share) bool {
			return s.Writable() && s.Has(shares.FindingWorldWritable) &&
				s.Dir.Mode == "0777"
		}, "the writable share somebody fixed with chmod 777"},
		{"archive", func(s shares.Share) bool {
			return s.Has(shares.FindingPathMissing) && !s.Dir.Exists
		}, "the share whose directory is gone"},
	}
	for _, check := range checks {
		share, ok := byName[check.name]
		if !ok {
			t.Errorf("the sample server has no [%s] (%s)", check.name, check.why)
			continue
		}
		if !check.want(share) {
			t.Errorf("[%s] is not %s: %+v", check.name, check.why, share.Findings)
		}
	}

	// Three accounts, one of them disabled.
	var disabled int
	for _, user := range a.model.Users {
		if user.Disabled {
			disabled++
		}
	}
	if len(a.model.Users) != 3 || disabled != 1 {
		t.Errorf("accounts = %d, disabled = %d", len(a.model.Users), disabled)
	}

	// Two clients with a file open each, and SMB1 off.
	if len(a.model.Sessions) != 2 || len(a.model.OpenFiles) != 2 {
		t.Errorf("sessions = %d, open files = %d", len(a.model.Sessions),
			len(a.model.OpenFiles))
	}
	if a.model.Global.SMB1Enabled {
		t.Errorf("the sample server should have SMB1 off")
	}
	// The open files are attached to the share they were reached through,
	// which smbstatus does not say.
	if a.model.FilesOn("team") != 1 || a.model.FilesOn("public") != 1 {
		t.Errorf("the open files were not matched to their shares")
	}
}

// TestActionsPreviewExactlyWhatTheyRun is the family's central promise, as a
// test: for every action key, the command line in the confirm dialog is the
// command line the backend is then asked to run.
func TestActionsPreviewExactlyWhatTheyRun(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		setup func(*testing.T, *app)
		want  string
	}{
		{
			name:  "reload the configuration",
			key:   "r",
			setup: func(t *testing.T, a *app) { gotoScreen(t, a, screenServer) },
			want:  "sudo -n smbcontrol all reload-config",
		},
		{
			name: "disable an account",
			key:  "D",
			setup: func(t *testing.T, a *app) {
				selectUser(t, a, "alice")
			},
			want: "sudo -n smbpasswd -d alice",
		},
		{
			name: "enable an account",
			key:  "E",
			setup: func(t *testing.T, a *app) {
				selectUser(t, a, "carol")
			},
			want: "sudo -n smbpasswd -e carol",
		},
		{
			name: "remove an account",
			key:  "x",
			setup: func(t *testing.T, a *app) {
				selectUser(t, a, "carol")
			},
			want: "sudo -n smbpasswd -x carol",
		},
		{
			name:  "ask the server what a client sees",
			key:   "t",
			setup: func(t *testing.T, a *app) { gotoScreen(t, a, screenServer) },
			want:  "sudo -n smbclient -L fileserver -N",
		},
	}
	for _, test := range tests {
		a, backend := newTestApp(t)
		test.setup(t, a)

		drain(t, a, press(a, test.key))
		if a.mode != modeConfirm {
			t.Fatalf("%s: no confirm dialog opened (status: %s)", test.name, a.status)
		}
		if a.confirm.Command != test.want {
			t.Errorf("%s: previewed %q, want %q", test.name, a.confirm.Command,
				test.want)
		}

		drain(t, a, press(a, "y"))
		ran := backend.Ran()
		if len(ran) != 1 {
			t.Fatalf("%s: ran %d commands, want 1", test.name, len(ran))
		}
		if got := backend.Preview(ran[0]); got != test.want {
			t.Errorf("%s: ran %q, want the previewed %q", test.name, got, test.want)
		}
	}
}

func TestCancellingRunsNothing(t *testing.T) {
	a, backend := newTestApp(t)
	gotoScreen(t, a, screenServer)
	drain(t, a, press(a, "r"))
	drain(t, a, press(a, "n"))

	if len(backend.Ran()) != 0 {
		t.Errorf("answering no ran %d commands", len(backend.Ran()))
	}
	if a.status != "cancelled" {
		t.Errorf("status = %q, want cancelled", a.status)
	}
}

// TestEditingAShareIsStagedCheckedAndDiffed covers the whole write path: the
// form collects, the backend stages, Samba's own parser reads it back, and the
// dialog shows the diff before anything is installed.
func TestEditingAShareIsStagedCheckedAndDiffed(t *testing.T) {
	a, backend := newTestApp(t)
	selectShare(t, a, "team")
	drain(t, a, press(a, "e"))
	if a.mode != modeForm {
		t.Fatalf("e did not open the editor (status: %s)", a.status)
	}
	// The name of an existing share is shown and not editable.
	if a.form.original != "team" {
		t.Errorf("the form is editing %q", a.form.original)
	}
	if !a.form.fields[0].locked {
		t.Errorf("the name field of an existing share is editable")
	}

	setField(&a.form, fieldComment, "Shared working files, second floor")
	drain(t, a, press(a, "enter"))
	if a.mode != modeConfirm {
		t.Fatalf("the form did not reach a confirm dialog (status: %s)", a.status)
	}

	// Samba read the staged file before anything was asked.
	if !strings.Contains(a.confirm.Body, "Samba's own parser") {
		t.Errorf("the dialog does not say the file was checked:\n%s", a.confirm.Body)
	}
	if !strings.Contains(a.confirm.Body, "testparm -s /tmp/tui-samba-") {
		t.Errorf("the dialog does not show the check command:\n%s", a.confirm.Body)
	}
	// The change is shown as a diff of the file it lands in.
	if !strings.Contains(a.confirm.Body, "+\tcomment = Shared working files, second floor") {
		t.Errorf("the diff does not show the change:\n%s", a.confirm.Body)
	}
	// [team] lives in the drop-in on the sample server, so that is where it is
	// written — and the include line is already there, so smb.conf is not
	// touched.
	if !strings.Contains(a.confirm.Command,
		"install -m 644 /tmp/tui-samba-") ||
		!strings.Contains(a.confirm.Command, "/etc/samba/tui-samba.d/team.conf") {
		t.Errorf("commands = %q", a.confirm.Command)
	}
	if strings.Contains(a.confirm.Command, "/etc/samba/smb.conf") {
		t.Errorf("smb.conf was rewritten for a share that is not in it: %q",
			a.confirm.Command)
	}
	// And it says who is on the share right now, which is what a reader wants
	// before answering yes.
	if !strings.Contains(a.confirm.Body, "Right now 1 client") {
		t.Errorf("the dialog does not say the share is in use:\n%s", a.confirm.Body)
	}

	lines := strings.Split(a.confirm.Command, "\n")
	drain(t, a, press(a, "y"))
	ran := backend.Ran()
	if len(ran) != len(lines) {
		t.Fatalf("ran %d commands, previewed %d", len(ran), len(lines))
	}
	for i, cmd := range ran {
		want := strings.TrimPrefix(lines[i], "$ ")
		if got := backend.Preview(cmd); got != want {
			t.Errorf("command %d ran %q, want the previewed %q", i, got, want)
		}
	}
	// The sample server now holds it, the way a real one would.
	share, ok := a.model.Share("team")
	if !ok || share.Comment != "Shared working files, second floor" {
		t.Errorf("the change did not reach the sample server: %+v", share)
	}
}

// TestANewShareAlsoAddsTheOneIncludeLine: a drop-in Samba is not told to read
// is a file that does nothing, so the line that reaches it is part of the same
// change.
func TestANewShareAlsoAddsTheOneIncludeLine(t *testing.T) {
	a, backend := newTestApp(t)
	drain(t, a, press(a, "n"))
	if a.mode != modeForm {
		t.Fatalf("n did not open the editor (status: %s)", a.status)
	}
	setField(&a.form, fieldName, "photos")
	setField(&a.form, fieldPath, "/srv/photos")
	drain(t, a, press(a, "enter"))
	if a.mode != modeConfirm {
		t.Fatalf("the form did not reach a confirm dialog (status: %s)", a.status)
	}

	if !strings.Contains(a.confirm.Command,
		"/etc/samba/tui-samba.d/photos.conf") {
		t.Errorf("the share was not written to a drop-in: %q", a.confirm.Command)
	}
	if !strings.Contains(a.confirm.Command, "/etc/samba/smb.conf") {
		t.Errorf("the include line was not added to smb.conf: %q",
			a.confirm.Command)
	}
	if !strings.Contains(a.confirm.Body,
		"include = /etc/samba/tui-samba.d/photos.conf") {
		t.Errorf("the dialog does not explain the include line:\n%s",
			a.confirm.Body)
	}

	drain(t, a, press(a, "y"))
	if len(backend.Ran()) < 2 {
		t.Fatalf("ran %d commands", len(backend.Ran()))
	}
	if _, ok := a.model.Share("photos"); !ok {
		t.Errorf("the new share is not in the inventory")
	}
	// And it really is reached through the include, because the model was
	// rebuilt by expanding smb.conf the way testparm does.
	var included bool
	for _, include := range a.model.Global.Includes {
		if include == "/etc/samba/tui-samba.d/photos.conf" {
			included = true
		}
	}
	if !included {
		t.Errorf("smb.conf does not include the new drop-in: %v",
			a.model.Global.Includes)
	}
}

// TestTheEditorRefusesSambaSOwnSections: [homes] and [printers] are shares
// whose meaning comes from Samba, and a form with a path field has nothing
// useful to say about either.
func TestTheEditorRefusesSambaSOwnSections(t *testing.T) {
	a, _ := newTestApp(t)
	selectShare(t, a, "homes")
	drain(t, a, press(a, "e"))
	if a.mode == modeForm {
		t.Errorf("the editor opened on [homes]")
	}
	if !strings.Contains(a.status, "Samba's own sections") {
		t.Errorf("status = %q", a.status)
	}
}

func TestTheFormRefusesAPathThatWouldReachTheFile(t *testing.T) {
	a, backend := newTestApp(t)
	drain(t, a, press(a, "n"))
	setField(&a.form, fieldName, "evil")
	setField(&a.form, fieldPath, "/srv/x\n[global]\n\tsecurity = share")
	drain(t, a, press(a, "enter"))

	if a.mode == modeConfirm {
		t.Errorf("the form accepted a path carrying a second section")
	}
	if a.status == "" {
		t.Errorf("the form refused silently")
	}
	if len(backend.Ran()) != 0 {
		t.Errorf("a command ran anyway")
	}
}

// TestTheFormWarnsAsTheMistakeBecomesTrue: the combination that turns a file
// server into a drop box is said while it is being chosen, not after it is
// written.
func TestTheFormWarnsAsTheMistakeBecomesTrue(t *testing.T) {
	a, _ := newTestApp(t)
	drain(t, a, press(a, "n"))
	a.form.values[fieldGuest] = "yes"
	a.form.values[fieldReadOnly] = "no"
	if warning := a.form.warning(); !strings.Contains(warning, "no password") {
		t.Errorf("warning = %q", warning)
	}
	a.form.values[fieldGuest] = "no"
	if warning := a.form.warning(); !strings.Contains(warning, "every account") {
		t.Errorf("warning = %q", warning)
	}
}

// TestAddingAnAccountNeverPutsThePasswordInACommandLine is the promise the
// account screen makes, checked at the level a user sees it.
func TestAddingAnAccountNeverPutsThePasswordInACommandLine(t *testing.T) {
	const password = "hunter2-and-then-some"
	a, backend := newTestApp(t)
	gotoScreen(t, a, screenUsers)

	drain(t, a, press(a, "a"))
	if a.mode != modeInput {
		t.Fatalf("a did not ask for a name (status: %s)", a.status)
	}
	typeIn(t, a, "dave")
	drain(t, a, press(a, "enter"))

	if a.mode != modeInput || a.promptFor != promptPassword {
		t.Fatalf("the name was not followed by a password prompt (mode %v)", a.mode)
	}
	// It is masked on the way in.
	if a.input.Model.EchoMode != maskedEcho {
		t.Errorf("the password prompt is not masked")
	}
	typeIn(t, a, password)
	drain(t, a, press(a, "enter"))

	if a.mode != modeConfirm {
		t.Fatalf("no confirm dialog opened (status: %s)", a.status)
	}
	if a.confirm.Command != "sudo -n smbpasswd -a -s dave" {
		t.Errorf("previewed %q", a.confirm.Command)
	}
	if strings.Contains(a.confirm.Command, password) ||
		strings.Contains(a.confirm.Body, password) {
		t.Errorf("the password reached the dialog")
	}

	drain(t, a, press(a, "y"))
	ran := backend.Ran()
	if len(ran) != 1 {
		t.Fatalf("ran %d commands", len(ran))
	}
	if strings.Contains(ran[0].String(), password) {
		t.Errorf("the password reached the command line: %s", ran[0].String())
	}
	if ran[0].Stdin != password+"\n"+password+"\n" {
		t.Errorf("the password did not go to standard input")
	}
	if _, ok := a.model.User("dave"); !ok {
		t.Errorf("the account is not in the sample database")
	}
}

func TestRemovingAnAccountSaysTheUnixOneSurvives(t *testing.T) {
	a, _ := newTestApp(t)
	selectUser(t, a, "carol")
	drain(t, a, press(a, "x"))
	if a.mode != modeConfirm {
		t.Fatalf("x did not open a confirm dialog (status: %s)", a.status)
	}
	if !strings.Contains(a.confirm.Body, "Unix account") {
		t.Errorf("the dialog does not say what survives:\n%s", a.confirm.Body)
	}
	if !a.confirm.Danger {
		t.Errorf("removing an account must be painted as dangerous")
	}
}

func TestReloadIsNotPaintedAsDangerous(t *testing.T) {
	a, _ := newTestApp(t)
	gotoScreen(t, a, screenServer)
	drain(t, a, press(a, "r"))
	if a.mode != modeConfirm {
		t.Fatalf("r did not open a confirm dialog (status: %s)", a.status)
	}
	if a.confirm.Danger {
		t.Errorf("a reload that disconnects nobody was painted as dangerous")
	}
	if !strings.Contains(a.confirm.Body, "not a restart") {
		t.Errorf("the dialog does not distinguish a reload from a restart:\n%s",
			a.confirm.Body)
	}
}

// TestTheSelfTestAnswerIsKept: what `smbclient -L` prints is the whole point
// of running it, so it is shown rather than summarised into a status line.
func TestTheSelfTestAnswerIsKept(t *testing.T) {
	a, _ := newTestApp(t)
	gotoScreen(t, a, screenServer)
	drain(t, a, press(a, "t"))
	drain(t, a, press(a, "y"))

	if !strings.Contains(a.selfTest, "Sharename") {
		t.Fatalf("the answer was not kept: %q", a.selfTest)
	}
	if a.screen != screenServer {
		t.Errorf("the answer did not bring the server screen forward")
	}
	// A share that is not browseable is real and is not in a client's listing,
	// which is the difference this key exists to show.
	if strings.Contains(a.selfTest, "homes") {
		t.Errorf("a non-browseable share appeared in the client's listing")
	}
	if !strings.Contains(a.View(), "smbclient -L") {
		t.Errorf("the answer is not on the server screen")
	}
}

func TestFilterMatchesEveryScreen(t *testing.T) {
	a, _ := newTestApp(t)
	a.filter = "team"
	a.applyFilter()
	if len(a.shareRows) != 1 {
		t.Errorf("the share filter matched %d rows, want 1", len(a.shareRows))
	}
	if len(a.connRows) == 0 {
		t.Errorf("the connections filter matched nothing")
	}

	a.filter = "carol"
	a.applyFilter()
	if len(a.userRows) != 1 {
		t.Errorf("the account filter matched %d rows", len(a.userRows))
	}

	a.filter = "workgroup"
	a.applyFilter()
	if len(a.serverRow) == 0 {
		t.Errorf("the server filter matched nothing")
	}

	a.filter = "nothing here"
	a.applyFilter()
	if len(a.shareRows)+len(a.userRows)+len(a.connRows)+len(a.serverRow) != 0 {
		t.Errorf("a filter that matches nothing kept rows")
	}
}

// TestEveryScreenHasADetail: enter must open something on all four, because a
// row a reader cannot open is a row whose truncated cells are all they get.
func TestEveryScreenHasADetail(t *testing.T) {
	for s := screen(0); s < screenCount; s++ {
		a, _ := newTestApp(t)
		gotoScreen(t, a, s)
		drain(t, a, press(a, "enter"))
		if a.mode != modeDetail {
			t.Fatalf("%s: enter opened nothing (status: %s)", s.title(), a.status)
		}
		if lines := a.detailLines(); len(lines) < 3 {
			t.Errorf("%s: the detail screen is %d lines", s.title(), len(lines))
		}
		drain(t, a, press(a, "esc"))
		if a.mode != modeBrowse {
			t.Errorf("%s: esc did not return to the table", s.title())
		}
	}
}

// TestShareDetailShowsTheEvidence: the row is a summary, and the detail is
// where the reason for it has to be.
func TestShareDetailShowsTheEvidence(t *testing.T) {
	a, _ := newTestApp(t)
	selectShare(t, a, "team")
	drain(t, a, press(a, "enter"))

	view := strings.Join(a.detailLines(), "\n")
	for _, want := range []string{
		"[team]",
		"/srv/team",
		"0777",
		"every account on this machine can write to it",
		"The directory itself",
		"In use right now",
		"Every effective parameter",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the detail screen is missing %q:\n%s", want, view)
		}
	}
}

// TestRendersAtEveryWidth is the responsive contract: from a narrow pane to a
// wide screen, no frame may wrap, because a wrapped row desynchronises Bubble
// Tea's line accounting and every frame after it lands in the wrong place.
func TestRendersAtEveryWidth(t *testing.T) {
	a, _ := newTestApp(t)

	for width := 40; width <= 200; width += 4 {
		a.width, a.height = width, 24
		a.clampCursor()

		for s := screen(0); s < screenCount; s++ {
			a.screen = s
			for _, m := range []mode{modeBrowse, modeDetail} {
				a.mode = m
				checkWidth(t, a, s.title(), width)
			}
		}

		a.mode = modeHelp
		checkWidth(t, a, "help", width)

		a.mode = modeForm
		a.form = newShareForm(a.model.Shares[0], false)
		checkWidth(t, a, "form", width)
	}
	a.mode = modeBrowse
}

// checkWidth renders the current frame and fails when a line overflows.
func checkWidth(t *testing.T, a *app, name string, width int) {
	t.Helper()
	for i, line := range strings.Split(a.View(), "\n") {
		if got := lineWidth(line); got > width {
			t.Fatalf("%s at %d cols: line %d is %d cells wide",
				name, width, i, got)
		}
	}
}

// lineWidth measures a rendered line, ignoring the ANSI escapes the theme adds.
func lineWidth(line string) int {
	width, inEscape := 0, false
	for _, r := range line {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K' || r == 'H'):
			inEscape = false
		case inEscape:
		default:
			width++
		}
	}
	return width
}

func TestBusyStateSwallowsInput(t *testing.T) {
	a, backend := newTestApp(t)
	gotoScreen(t, a, screenServer)
	a.busy = true
	drain(t, a, press(a, "r"))
	if a.mode != modeBrowse || len(backend.Ran()) != 0 {
		t.Errorf("a key pressed while a command runs must be ignored")
	}
}

// TestRemovingAShareTypesItsNameBackFirst covers the whole removal path: the
// deliberate step, the exact commands, and the share really going away.
func TestRemovingAShareTypesItsNameBackFirst(t *testing.T) {
	a, backend := newTestApp(t)
	selectShare(t, a, "team")
	drain(t, a, press(a, "X"))
	if a.mode != modeInput || a.promptFor != promptDeleteShare {
		t.Fatalf("X did not ask for the name (status: %s)", a.status)
	}

	typeIn(t, a, "team")
	drain(t, a, press(a, "enter"))
	if a.mode != modeConfirm {
		t.Fatalf("the name did not reach a confirm dialog (status: %s)", a.status)
	}
	if !a.confirm.Danger {
		t.Errorf("removing a share must be painted as dangerous")
	}

	// The three commands, in the order that never leaves smb.conf pointing at
	// a file that is gone.
	want := []string{
		"sudo -n install -m 644 ",
		"sudo -n rm -f -- /etc/samba/tui-samba.d/team.conf",
		"sudo -n smbcontrol all reload-config",
	}
	lines := strings.Split(a.confirm.Command, "\n$ ")
	if len(lines) != len(want) {
		t.Fatalf("previewed %d commands, want %d:\n%s", len(lines), len(want),
			a.confirm.Command)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("command %d is %q, want it to start with %q", i, lines[i],
				prefix)
		}
	}
	if !strings.Contains(lines[0], "/etc/samba/smb.conf") {
		t.Errorf("the include line is not removed from smb.conf: %q", lines[0])
	}
	// And the dialog says what a removal does not do.
	if !strings.Contains(a.confirm.Body, "it does not delete a single file") {
		t.Errorf("the dialog does not say the directory survives:\n%s",
			a.confirm.Body)
	}
	if !strings.Contains(a.confirm.Body, "-\tinclude = ") {
		t.Errorf("the diff does not show the include line going:\n%s",
			a.confirm.Body)
	}

	drain(t, a, press(a, "y"))
	ran := backend.Ran()
	if len(ran) != len(want) {
		t.Fatalf("ran %d commands, previewed %d", len(ran), len(want))
	}
	for i, cmd := range ran {
		if got := backend.Preview(cmd); got != lines[i] {
			t.Errorf("command %d ran %q, want the previewed %q", i, got, lines[i])
		}
	}
	if _, ok := a.model.Share("team"); ok {
		t.Errorf("the share is still in the inventory")
	}
	for _, include := range a.model.Global.Includes {
		if strings.Contains(include, "team.conf") {
			t.Errorf("smb.conf still includes the removed drop-in: %v",
				a.model.Global.Includes)
		}
	}
}

// TestTypingTheWrongNameRemovesNothing: the typed step is the whole point of
// the typed step.
func TestTypingTheWrongNameRemovesNothing(t *testing.T) {
	a, backend := newTestApp(t)
	selectShare(t, a, "team")
	drain(t, a, press(a, "X"))
	typeIn(t, a, "teamm")
	drain(t, a, press(a, "enter"))

	if a.mode == modeConfirm {
		t.Errorf("a mistyped name reached a confirm dialog")
	}
	if len(backend.Ran()) != 0 {
		t.Errorf("a command ran anyway")
	}
	if !strings.Contains(a.status, "nothing was removed") {
		t.Errorf("status = %q", a.status)
	}
}

// TestRemovingRefusesAShareThisToolDoesNotOwn: [public] is written in the
// sample machine's own smb.conf, so removing it would mean editing a file
// somebody else wrote.
func TestRemovingRefusesAShareThisToolDoesNotOwn(t *testing.T) {
	a, backend := newTestApp(t)
	selectShare(t, a, "public")
	drain(t, a, press(a, "X"))
	typeIn(t, a, "public")
	drain(t, a, press(a, "enter"))

	if a.mode == modeConfirm {
		t.Fatalf("a share defined in smb.conf reached a confirm dialog")
	}
	if !strings.Contains(a.status, "is written in /etc/samba/smb.conf itself") {
		t.Errorf("status = %q, want the specific reason", a.status)
	}
	if len(backend.Ran()) != 0 {
		t.Errorf("a command ran anyway")
	}
	if _, ok := a.model.Share("public"); !ok {
		t.Errorf("[public] left the inventory")
	}
}

// TestRemovingRefusesSambaSOwnSections: [homes] is not a directory export, and
// it is not this tool's to take away.
func TestRemovingRefusesSambaSOwnSections(t *testing.T) {
	a, _ := newTestApp(t)
	selectShare(t, a, "homes")
	drain(t, a, press(a, "X"))
	if a.mode == modeInput {
		t.Errorf("the removal prompt opened on [homes]")
	}
	if !strings.Contains(a.status, "Samba's own sections") {
		t.Errorf("status = %q", a.status)
	}
}

// TestGStaysTheFirstRowKey guards a navigation key the whole family shares.
// The server settings live on "o" precisely so that "g" can keep meaning what
// it means in vim and in every other tui-tools tool: jump to the first row.
func TestGStaysTheFirstRowKey(t *testing.T) {
	a, _ := newTestApp(t)
	gotoScreen(t, a, screenShares)
	if a.rowCount() < 2 {
		t.Fatalf("the sample machine needs at least two shares to move between")
	}
	drain(t, a, press(a, "j"))
	if a.cursor[a.screen] == 0 {
		t.Fatalf("the cursor did not leave the first row")
	}
	drain(t, a, press(a, "g"))
	if a.mode != modeBrowse {
		t.Fatalf("g opened %v instead of moving the cursor", a.mode)
	}
	if a.cursor[a.screen] != 0 || a.offset[a.screen] != 0 {
		t.Errorf("g left the cursor at %d (offset %d), want the first row",
			a.cursor[a.screen], a.offset[a.screen])
	}
}

// TestEditingTheServerSettingsIsStagedCheckedAndDiffed: the same path a share
// edit takes, on the three [global] parameters the form collects.
func TestEditingTheServerSettingsIsStagedCheckedAndDiffed(t *testing.T) {
	a, backend := newTestApp(t)
	gotoScreen(t, a, screenServer)
	drain(t, a, press(a, "o"))
	if a.mode != modeForm || a.form.kind != formGlobal {
		t.Fatalf("o did not open the server editor (status: %s)", a.status)
	}
	// It opens on what the server actually resolved, not on a blank form.
	if a.form.values[fieldWorkgroup] != "WORKGROUP" ||
		a.form.values[fieldMinProtocol] != "SMB2_02" {
		t.Errorf("the form was not seeded from the server: %v", a.form.values)
	}

	setField(&a.form, fieldWorkgroup, "OFFICE")
	setField(&a.form, fieldMinProtocol, "SMB3_11")
	setField(&a.form, fieldHostsAllow, "192.168.1. 127.")
	drain(t, a, press(a, "enter"))
	if a.mode != modeConfirm {
		t.Fatalf("the form did not reach a confirm dialog (status: %s)", a.status)
	}

	if !strings.Contains(a.confirm.Body, "Samba's own parser") {
		t.Errorf("the dialog does not say the file was checked:\n%s", a.confirm.Body)
	}
	for _, want := range []string{
		"+\tworkgroup = OFFICE",
		"+\tserver min protocol = SMB3_11",
		"+\thosts allow = 192.168.1. 127.",
	} {
		if !strings.Contains(a.confirm.Body, want) {
			t.Errorf("the diff is missing %q:\n%s", want, a.confirm.Body)
		}
	}
	if !strings.Contains(a.confirm.Command,
		"/etc/samba/tui-samba.d/tui-samba-global.conf") {
		t.Errorf("the settings were not written to the tool's own file: %q",
			a.confirm.Command)
	}
	if !strings.Contains(a.confirm.Command, "/etc/samba/smb.conf") {
		t.Errorf("the include line was not added to smb.conf: %q",
			a.confirm.Command)
	}
	if !strings.Contains(a.confirm.Body, "the ones that win") {
		t.Errorf("the dialog does not explain which settings win:\n%s",
			a.confirm.Body)
	}

	lines := strings.Split(a.confirm.Command, "\n$ ")
	drain(t, a, press(a, "y"))
	if len(backend.Ran()) != len(lines) {
		t.Fatalf("ran %d commands, previewed %d", len(backend.Ran()), len(lines))
	}
	// The sample machine now answers with them, through the same parser a real
	// one would.
	if a.model.Global.Workgroup != "OFFICE" {
		t.Errorf("the workgroup did not reach the sample server: %q",
			a.model.Global.Workgroup)
	}
	if a.model.Global.MinProtocol != "SMB3_11" {
		t.Errorf("the minimum protocol did not reach the sample server: %q",
			a.model.Global.MinProtocol)
	}
	if a.model.Global.Params["hosts allow"] != "192.168.1. 127." {
		t.Errorf("the host list did not reach the sample server: %q",
			a.model.Global.Params["hosts allow"])
	}
}

// TestTheServerFormWarnsAboutSMB1: NT1 is the one choice in that picker that
// makes the server worse, and it is said as it is chosen.
func TestTheServerFormWarnsAboutSMB1(t *testing.T) {
	a, _ := newTestApp(t)
	gotoScreen(t, a, screenServer)
	drain(t, a, press(a, "o"))
	a.form.values[fieldMinProtocol] = "NT1"
	if warning := a.form.warning(); !strings.Contains(warning, "SMB1") {
		t.Errorf("warning = %q", warning)
	}
}

func TestTheServerFormRefusesAWorkgroupThatWouldReachTheFile(t *testing.T) {
	a, backend := newTestApp(t)
	gotoScreen(t, a, screenServer)
	drain(t, a, press(a, "o"))
	setField(&a.form, fieldWorkgroup, "OFFICE\n\tsecurity = share")
	drain(t, a, press(a, "enter"))

	if a.mode == modeConfirm {
		t.Errorf("the form accepted a workgroup carrying a second parameter")
	}
	if len(backend.Ran()) != 0 {
		t.Errorf("a command ran anyway")
	}
}

// TestANewSharesDirectoryIsCreatedInTheSamePlan: a share whose path does not
// exist looks to every client like a permission problem, so the directory is
// offered in the change that creates the share rather than left as homework.
func TestANewSharesDirectoryIsCreatedInTheSamePlan(t *testing.T) {
	a, backend := newTestApp(t)
	drain(t, a, press(a, "n"))
	setField(&a.form, fieldName, "photos")
	setField(&a.form, fieldPath, "/srv/photos")
	setField(&a.form, fieldOwner, "root:staff")
	drain(t, a, press(a, "enter"))
	if a.mode != modeConfirm {
		t.Fatalf("the form did not reach a confirm dialog (status: %s)", a.status)
	}

	lines := strings.Split(a.confirm.Command, "\n$ ")
	if lines[0] != "sudo -n install -d -m 2775 -o root -g staff /srv/photos" {
		t.Errorf("the directory is not created first: %q", lines[0])
	}
	if !strings.Contains(a.confirm.Body, "/srv/photos does not exist yet") {
		t.Errorf("the dialog does not say why:\n%s", a.confirm.Body)
	}
	// SELinux is off on the sample machine, so no label is asked for.
	if strings.Contains(a.confirm.Command, "chcon") {
		t.Errorf("a label was built on a machine with no SELinux: %q",
			a.confirm.Command)
	}

	drain(t, a, press(a, "y"))
	if len(backend.Ran()) != len(lines) {
		t.Fatalf("ran %d commands, previewed %d", len(backend.Ran()), len(lines))
	}
	share, ok := a.model.Share("photos")
	if !ok {
		t.Fatalf("the new share is not in the inventory")
	}
	if !share.Dir.Exists || share.Dir.Mode != "2775" {
		t.Errorf("the directory was not created: %+v", share.Dir)
	}
	if share.Has(shares.FindingPathMissing) {
		t.Errorf("the new share still reports a missing path")
	}
}

// TestAnExistingPathIsNeverTouched: the mode and the owner of a directory that
// is already there are somebody's decision, and a share form does not overrule
// them.
func TestAnExistingPathIsNeverTouched(t *testing.T) {
	a, _ := newTestApp(t)
	drain(t, a, press(a, "n"))
	setField(&a.form, fieldName, "public2")
	setField(&a.form, fieldPath, "/srv/public")
	drain(t, a, press(a, "enter"))
	if a.mode != modeConfirm {
		t.Fatalf("the form did not reach a confirm dialog (status: %s)", a.status)
	}
	if strings.Contains(a.confirm.Command, "install -d -m 2775") {
		t.Errorf("an existing directory was re-created: %q", a.confirm.Command)
	}
}

// TestTheServerFormRendersAtEveryWidth: the responsive contract covers the
// second form too, which has fields the share form does not.
func TestTheServerFormRendersAtEveryWidth(t *testing.T) {
	a, _ := newTestApp(t)
	a.mode = modeForm
	a.form = newGlobalForm(a.model.Global)
	for width := 40; width <= 200; width += 4 {
		a.width, a.height = width, 24
		checkWidth(t, a, "server form", width)
	}
	a.mode = modeBrowse
}

func TestSplitOwner(t *testing.T) {
	tests := []struct {
		value string
		owner string
		group string
	}{
		{"", "root", "root"},
		{"alice", "alice", "alice"},
		{"alice:staff", "alice", "staff"},
		{" alice : staff ", "alice", "staff"},
		// A trailing colon means the group was left out, which is what
		// `chown alice` means too.
		{"alice:", "alice", "alice"},
	}
	for _, test := range tests {
		owner, group := splitOwner(test.value)
		if owner != test.owner || group != test.group {
			t.Errorf("splitOwner(%q) = %q, %q, want %q, %q", test.value, owner,
				group, test.owner, test.group)
		}
	}
}
