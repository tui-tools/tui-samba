package main

import (
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
func setField(f *shareForm, key, value string) {
	f.values[key] = value
	f.focusActive()
}

// gotoScreen moves to a tab by its number key.
func gotoScreen(t *testing.T, a *app, s screen) {
	t.Helper()
	drain(t, a, press(a, string(rune('1'+int(s)))))
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
