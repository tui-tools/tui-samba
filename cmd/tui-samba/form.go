package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tui-tools/tui-kit/theme"
	"github.com/tui-tools/tui-kit/ui"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// maskedEcho is how a password prompt renders what is typed. It is named here
// so app.go does not have to import the text input package for one constant.
const maskedEcho = textinput.EchoPassword

// The fields of the share form, named rather than numbered so the picker knows
// which one it is filling.
const (
	fieldName       = "name"
	fieldPath       = "path"
	fieldComment    = "comment"
	fieldBrowseable = "browseable"
	fieldReadOnly   = "readonly"
	fieldGuest      = "guest"
	fieldValidUsers = "validusers"
	fieldWriteList  = "writelist"
	fieldCreateMask = "createmask"
	fieldDirMask    = "dirmask"
)

// yesNoOptions is the closed set a boolean field offers, in the order it
// offers them: the safer answer first, so a field left alone is the safe one.
var yesNoOptions = []string{"no", "yes"}

// formField is one row of the form.
type formField struct {
	key   string
	label string
	// options is the closed set of values, nil for a free-text field.
	options []string
	help    string
	// locked marks a field that is shown and cannot be edited.
	locked bool
}

// choice reports whether the field is one the picker serves.
func (f formField) choice() bool { return len(f.options) > 0 && !f.locked }

// shareForm is the guided editor for one share.
//
// Ten fields, and no free-text escape hatch for the rest. A share can carry a
// hundred parameters and this form collects the ten people actually change;
// every other line the stanza already had is kept exactly as it is, so a share
// with a `vfs objects` or a `hosts allow` survives an edit here untouched. A
// form that could set any parameter would be a text editor with extra steps,
// and a text editor is what `$EDITOR /etc/samba/smb.conf` already is.
type shareForm struct {
	fields []formField
	values map[string]string
	active int
	input  textinput.Model
	// original is the share being edited, empty when creating one.
	original string
	// creating reports which of the two things this form is.
	creating bool
}

// newShareForm builds the editor, seeded from the share it is editing.
func newShareForm(share shares.Share, creating bool) shareForm {
	f := shareForm{
		creating: creating,
		values: map[string]string{
			fieldName:       share.Name,
			fieldPath:       share.Path,
			fieldComment:    share.Comment,
			fieldBrowseable: yesNo(share.Browseable),
			fieldReadOnly:   yesNo(share.ReadOnly),
			fieldGuest:      yesNo(share.GuestOK),
			fieldValidUsers: strings.Join(share.ValidUsers, " "),
			fieldWriteList:  strings.Join(share.WriteList, " "),
			fieldCreateMask: share.CreateMask,
			fieldDirMask:    share.DirectoryMask,
		},
	}
	if creating {
		f.original = ""
		f.values[fieldBrowseable] = "yes"
		f.values[fieldReadOnly] = "yes"
		f.values[fieldGuest] = "no"
		f.values[fieldCreateMask] = "0664"
		f.values[fieldDirMask] = "0775"
	} else {
		f.original = share.Name
	}

	// The name is fixed once a share exists. Renaming is two changes and the
	// second one is deleting a share, which is not something to do behind a
	// form field somebody tabbed through.
	f.fields = []formField{
		{key: fieldName, label: "Name", locked: !creating,
			help: nameHelp(creating)},
		{key: fieldPath, label: "Path",
			help: "The directory this share exports. It has to exist, and its " +
				"Unix permissions decide what is really possible in it."},
		{key: fieldComment, label: "Comment",
			help: "What a client sees beside the name in a share listing."},
		{key: fieldBrowseable, label: "Browseable", options: yesNoOptions,
			help: "Whether the share appears in a listing. A share that does " +
				"not is still reachable by name — this hides it, it does not " +
				"protect it."},
		{key: fieldReadOnly, label: "Read only", options: yesNoOptions,
			help: "`read only = yes` is the safe default. Writing also needs " +
				"the Unix permissions to allow it, whatever this says."},
		{key: fieldGuest, label: "Guest ok", options: yesNoOptions,
			help: "Whether a client with no password gets in. Together with " +
				"read only = no, that is anybody on the network writing here."},
		{key: fieldValidUsers, label: "Valid users",
			help: "Who may connect at all. Space separated; @group for a " +
				"group. Empty means every account in the Samba database."},
		{key: fieldWriteList, label: "Write list",
			help: "Who may write even on a read-only share. Space separated."},
		{key: fieldCreateMask, label: "Create mask",
			help: "The mode a new file gets, in octal. 0664 lets the group " +
				"write; 0644 does not."},
		{key: fieldDirMask, label: "Directory mask",
			help: "The mode a new directory gets. 2775 sets the setgid bit, " +
				"which is what keeps a shared directory's group on everything " +
				"created in it."},
	}

	f.input = textinput.New()
	f.input.CharLimit = 200
	f.input.Prompt = ""
	f.focusActive()
	return f
}

// nameHelp is what the name field says, which is different for the two things
// this form is.
func nameHelp(creating bool) string {
	if creating {
		return "What clients will type after the server name. It is also the " +
			"file name in the drop-in directory, so letters, digits, dot, dash " +
			"and underscore only."
	}
	return "The name of an existing share cannot be changed here: a rename is " +
		"really a create and a delete, and deleting a share is not something " +
		"to do behind a form."
}

// yesNo renders a boolean the way the form and the file both spell it.
func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// visible are the fields the form is showing, which is all of them.
func (f shareForm) visible() []formField { return f.fields }

// current is the field being edited.
func (f shareForm) current() formField {
	fields := f.visible()
	if f.active < 0 || f.active >= len(fields) {
		return formField{}
	}
	return fields[f.active]
}

// focusActive loads the active field into the text box, or blurs it for a
// choice or a locked field.
func (f *shareForm) focusActive() {
	field := f.current()
	if field.choice() || field.locked || field.key == "" {
		f.input.Blur()
		return
	}
	f.input.SetValue(f.values[field.key])
	f.input.Focus()
	f.input.CursorEnd()
}

// save writes the text box back into the values before the field changes.
func (f *shareForm) save() {
	field := f.current()
	if field.key != "" && !field.choice() && !field.locked {
		f.values[field.key] = f.input.Value()
	}
}

// next moves to the following field.
func (f *shareForm) next() {
	f.save()
	f.active = (f.active + 1) % len(f.visible())
	f.focusActive()
}

// prev moves to the previous field.
func (f *shareForm) prev() {
	f.save()
	count := len(f.visible())
	f.active = (f.active + count - 1) % count
	f.focusActive()
}

// activeIsChoice reports whether the active field is one the picker serves.
func (f shareForm) activeIsChoice() bool { return f.current().choice() }

// activeKey, activeLabel, activeOptions and activeValue expose the active
// field to the picker dialog.
func (f shareForm) activeKey() string       { return f.current().key }
func (f shareForm) activeLabel() string     { return f.current().label }
func (f shareForm) activeOptions() []string { return f.current().options }
func (f shareForm) activeValue() string     { return f.values[f.current().key] }

// set applies a value chosen in the picker to a field.
func (f *shareForm) set(field, value string) {
	if field == "" {
		return
	}
	f.values[field] = value
	f.focusActive()
}

// cycle moves a choice field one step.
func (f *shareForm) cycle(delta int) {
	field := f.current()
	if !field.choice() {
		return
	}
	index := 0
	for i, option := range field.options {
		if option == f.values[field.key] {
			index = i
		}
	}
	index = (index + delta + len(field.options)) % len(field.options)
	f.set(field.key, field.options[index])
}

// updateActive forwards a message to the value field when it is a text box.
func (f *shareForm) updateActive(msg tea.Msg) tea.Cmd {
	if f.current().choice() || f.current().locked {
		return nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

// request is what the form collected, ready for the backend to render into a
// stanza. Only the collecting lives here: what a name, a path and an access
// list may be is the backend's rule, checked once, where the file is built.
func (f *shareForm) request() (shares.ShareRequest, error) {
	f.save()
	return shares.ShareRequest{
		Original:      f.original,
		Name:          strings.TrimSpace(f.values[fieldName]),
		Path:          strings.TrimSpace(f.values[fieldPath]),
		Comment:       strings.TrimSpace(f.values[fieldComment]),
		Browseable:    f.values[fieldBrowseable],
		ReadOnly:      f.values[fieldReadOnly],
		GuestOK:       f.values[fieldGuest],
		ValidUsers:    f.values[fieldValidUsers],
		WriteList:     f.values[fieldWriteList],
		CreateMask:    strings.TrimSpace(f.values[fieldCreateMask]),
		DirectoryMask: strings.TrimSpace(f.values[fieldDirMask]),
	}, nil
}

// warning is the one line the form keeps under the fields: the combination
// that turns a file server into a drop box, said as it becomes true rather
// than after the change is written.
func (f shareForm) warning() string {
	if f.values[fieldGuest] == "yes" && f.values[fieldReadOnly] == "no" {
		return "guest ok = yes with read only = no: anybody who can reach " +
			"this server can write here, with no password"
	}
	if f.values[fieldReadOnly] == "no" &&
		strings.TrimSpace(f.values[fieldValidUsers]) == "" &&
		strings.TrimSpace(f.values[fieldWriteList]) == "" {
		return "writable with no valid users and no write list: every account " +
			"in the Samba database can write here"
	}
	return ""
}

// view renders the form as a dialog.
func (f shareForm) view(t theme.Theme, width, height int) string {
	inner := min(max(width-8, 34), 76)
	labelWidth := min(14, max(inner-16, 8))
	valueWidth := max(inner-labelWidth-6, 10)

	title := "Edit the share [" + f.original + "]"
	if f.creating {
		title = "Add a share"
	}
	lines := []string{t.Title.Render(ui.Truncate(title, inner-2)), ""}

	for i, field := range f.visible() {
		label := t.Muted.Render(ui.Pad(ui.Truncate(field.label, labelWidth),
			labelWidth))
		var value string
		switch {
		case field.locked:
			value = t.Muted.Render(ui.Truncate(orPlaceholder(f.values[field.key]),
				valueWidth))
		case field.choice():
			value = renderChoice(t, f.values[field.key], i == f.active, valueWidth)
		case i == f.active:
			input := f.input
			input.Width = valueWidth - 2
			value = input.View()
		default:
			value = t.Base.Render(ui.Truncate(orPlaceholder(f.values[field.key]),
				valueWidth))
		}
		marker := "  "
		if i == f.active {
			marker = t.Accent.Render("> ")
		}
		lines = append(lines, marker+label+"  "+value)
	}

	if help := f.current().help; help != "" {
		lines = append(lines, "", t.Muted.Render(help))
	}
	if warning := f.warning(); warning != "" {
		lines = append(lines, "", t.Warn.Render(warning))
	}
	lines = append(lines, "",
		t.Muted.Render(ui.Truncate(
			"enter stages the file and has Samba read it back before anything "+
				"is written.", inner-4)),
		"",
		t.Key.Render("tab")+t.KeyDesc.Render(" next  ")+
			t.Key.Render("←/→")+t.KeyDesc.Render(" change  ")+
			t.Key.Render("space")+t.KeyDesc.Render(" list  ")+
			t.Key.Render("enter")+t.KeyDesc.Render(" review  ")+
			t.Key.Render("esc")+t.KeyDesc.Render(" cancel"))

	box := t.Dialog.Width(inner).Render(strings.Join(lines, "\n"))
	return placeCenter(box, width, height)
}

// orPlaceholder renders an empty value as something visible, so a blank row is
// never mistaken for a broken one.
func orPlaceholder(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// renderChoice draws a choice field with its cycling arrows.
func renderChoice(t theme.Theme, value string, active bool, width int) string {
	value = ui.Truncate(orPlaceholder(value), width-4)
	if active {
		return t.Accent.Render("‹ ") + t.Base.Render(value) + t.Accent.Render(" ›")
	}
	return t.Base.Render("  " + value)
}
