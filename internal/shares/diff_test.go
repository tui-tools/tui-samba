package shares

import (
	"strings"
	"testing"
)

// TestDiffShowsOnlyWhatChanged is why this is a real line diff rather than
// "everything out, everything in": the confirm dialog for a share edit has one
// job, and a diff that repeats the whole smb.conf buries it.
func TestDiffShowsOnlyWhatChanged(t *testing.T) {
	before := "[global]\n\tworkgroup = WG\n\n[team]\n\tpath = /srv/team\n" +
		"\tread only = Yes\n\n[public]\n\tpath = /srv/public\n"
	after := strings.Replace(before, "\tread only = Yes", "\tread only = No", 1)

	diff := Diff("/etc/samba/smb.conf", before, after)
	if !strings.Contains(diff, "-\tread only = Yes") ||
		!strings.Contains(diff, "+\tread only = No") {
		t.Fatalf("the changed line is not in the diff:\n%s", diff)
	}
	// Only the changed line and its two lines of context: the sections above
	// and below are not repeated.
	if strings.Contains(diff, "workgroup = WG") ||
		strings.Contains(diff, "/srv/public") {
		t.Errorf("an unchanged section is in the diff:\n%s", diff)
	}
	if !strings.HasPrefix(diff, "--- /etc/samba/smb.conf") {
		t.Errorf("no unified diff header:\n%s", diff)
	}
}

func TestDiffOfANewFileNamesDevNull(t *testing.T) {
	diff := Diff("/etc/samba/tui-samba.d/team.conf", "", "[team]\n")
	if !strings.HasPrefix(diff, "--- /dev/null") {
		t.Errorf("a file that does not exist yet is not marked as new:\n%s", diff)
	}
}

func TestDiffOfNoChangeIsEmpty(t *testing.T) {
	if got := Diff("/etc/samba/smb.conf", "[team]\n", "[team]\n"); got != "" {
		t.Errorf("Diff = %q, want empty", got)
	}
}
