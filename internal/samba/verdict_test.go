package samba

import (
	"strings"
	"testing"

	"github.com/tui-tools/tui-samba/internal/shares"
)

// has reports whether a share carries a finding of one kind.
func has(share shares.Share, kind string) bool { return share.Has(kind) }

// TestJudgeShare covers each finding on the share that deserves it, and — as
// much as the finding itself — the ordinary shares that must raise nothing. A
// list that flags what everybody runs teaches people to stop reading the list.
func TestJudgeShare(t *testing.T) {
	global := shares.Global{Params: map[string]string{}}

	tests := []struct {
		name  string
		share shares.Share
		want  string
		// verdict is what the row is sorted and painted by.
		verdict shares.Verdict
	}{
		{
			name: "a path that is not there",
			share: shares.Share{Name: "archive", Path: "/srv/archive",
				ReadOnly: true,
				Dir:      shares.DirInfo{Path: "/srv/archive", Note: "no such directory"}},
			want:    shares.FindingPathMissing,
			verdict: shares.VerdictRisk,
		},
		{
			name: "a directory anybody can write to",
			share: shares.Share{Name: "team", Path: "/srv/team",
				ValidUsers: []string{"alice"},
				Dir: shares.DirInfo{Path: "/srv/team", Exists: true, IsDir: true,
					Mode: "0777", WorldWritable: true}},
			want:    shares.FindingWorldWritable,
			verdict: shares.VerdictRisk,
		},
		{
			name: "writable and open to guests",
			share: shares.Share{Name: "drop", Path: "/srv/drop", GuestOK: true,
				Dir: shares.DirInfo{Path: "/srv/drop", Exists: true, IsDir: true,
					Mode: "0755"}},
			want:    shares.FindingGuestWritable,
			verdict: shares.VerdictRisk,
		},
		{
			name: "writable with no list at all",
			share: shares.Share{Name: "scratch", Path: "/srv/scratch",
				Dir: shares.DirInfo{Path: "/srv/scratch", Exists: true, IsDir: true,
					Mode: "0755"}},
			want:    shares.FindingNoAccessControl,
			verdict: shares.VerdictWarn,
		},
		{
			name: "an ordinary read-only share raises nothing",
			share: shares.Share{Name: "public", Path: "/srv/public",
				ReadOnly: true, GuestOK: true,
				Dir: shares.DirInfo{Path: "/srv/public", Exists: true, IsDir: true,
					Mode: "0755"}},
			want:    "",
			verdict: shares.VerdictOK,
		},
		{
			name: "a writable share with a valid users list raises nothing",
			share: shares.Share{Name: "team", Path: "/srv/team",
				ValidUsers: []string{"alice", "bob"},
				Dir: shares.DirInfo{Path: "/srv/team", Exists: true, IsDir: true,
					Mode: "2770"}},
			want:    "",
			verdict: shares.VerdictOK,
		},
		{
			name: "[homes] is on by default everywhere and is not a mistake",
			share: shares.Share{Name: "homes", Special: true,
				ValidUsers: []string{"%S"}},
			want:    "",
			verdict: shares.VerdictOK,
		},
	}

	for _, test := range tests {
		judged := JudgeShare(test.share, global)
		if test.want == "" {
			if len(judged.Findings) != 0 {
				t.Errorf("%s: raised %+v", test.name, judged.Findings)
			}
		} else if !has(judged, test.want) {
			t.Errorf("%s: did not raise %s, raised %+v", test.name, test.want,
				judged.Findings)
		}
		if judged.Verdict != test.verdict {
			t.Errorf("%s: verdict = %q, want %q", test.name, judged.Verdict,
				test.verdict)
		}
	}
}

// TestGuestShareWithBadUserMapping: a typo in a username getting in anyway is
// the case this pair of settings produces, and neither of them is a finding on
// its own.
func TestGuestShareWithBadUserMapping(t *testing.T) {
	global := shares.Global{MapToGuest: "Bad User",
		Params: map[string]string{"map to guest": "Bad User"}}
	share := shares.Share{Name: "public", Path: "/srv/public", ReadOnly: true,
		GuestOK: true,
		Dir:     shares.DirInfo{Path: "/srv/public", Exists: true, IsDir: true, Mode: "0755"}}

	judged := JudgeShare(share, global)
	if !has(judged, shares.FindingMapToGuest) {
		t.Errorf("the guest mapping was not raised: %+v", judged.Findings)
	}

	// Without a guest share the server-wide finding is not raised either: the
	// setting alone does nothing anybody can reach.
	if findings := JudgeGlobal(global, []shares.Share{
		{Name: "team", Path: "/srv/team"},
	}); len(findings) != 0 {
		t.Errorf("map to guest was raised on a server with no guest share: %+v",
			findings)
	}
	if findings := JudgeGlobal(global, []shares.Share{share}); len(findings) != 1 {
		t.Errorf("map to guest was not raised with a guest share: %+v", findings)
	}
}

func TestJudgeGlobal(t *testing.T) {
	// SMB1 is off by default from Samba 4.11, so a server below SMB2 is one
	// somebody set there on purpose.
	smb1 := shares.Global{MinProtocol: "NT1", SMB1Enabled: true,
		Params: map[string]string{"server min protocol": "NT1"}}
	findings := JudgeGlobal(smb1, nil)
	if len(findings) != 1 || findings[0].Kind != shares.FindingSMB1 {
		t.Fatalf("findings = %+v", findings)
	}
	if findings[0].Verdict != shares.VerdictRisk {
		t.Errorf("SMB1 is not a warning, it is a risk: %+v", findings[0])
	}
	if !strings.Contains(findings[0].Message, "4.11") {
		t.Errorf("the message does not say when the default changed: %q",
			findings[0].Message)
	}

	// `security = share` was removed in Samba 4.0 and is still copied out of
	// tutorials; a server carrying it is doing something other than what its
	// author meant.
	removed := shares.Global{Params: map[string]string{"security": "share"}}
	findings = JudgeGlobal(removed, nil)
	if len(findings) != 1 || findings[0].Kind != shares.FindingSecurityShare {
		t.Errorf("findings = %+v", findings)
	}

	// A server on the defaults says nothing.
	plain := shares.Global{MinProtocol: "SMB2_02",
		Params: map[string]string{"security": "USER"}}
	if findings := JudgeGlobal(plain, nil); len(findings) != 0 {
		t.Errorf("an ordinary server raised %+v", findings)
	}
}

func TestJudgeUser(t *testing.T) {
	// The one thing Samba itself never says: the entry maps to a Unix account
	// that is not there, so nobody can ever log in as it.
	orphan := JudgeUser(shares.User{Name: "ghost"})
	if !strings.Contains(orphan.Note, "no Unix account") {
		t.Errorf("note = %q", orphan.Note)
	}
	if !strings.Contains(orphan.Note, "tui-users") {
		t.Errorf("the note does not point at the tool for the job: %q", orphan.Note)
	}

	disabled := JudgeUser(shares.User{Name: "carol", UnixPresent: true,
		Disabled: true})
	if !strings.Contains(disabled.Note, "disabled") {
		t.Errorf("note = %q", disabled.Note)
	}

	ordinary := JudgeUser(shares.User{Name: "alice", UnixPresent: true})
	if ordinary.Note != "" {
		t.Errorf("an ordinary account was given a note: %q", ordinary.Note)
	}
}

// TestSortSharesIsFindingsFirst: a reader arrives with "what is wrong here",
// and the answer must not be somewhere in an alphabetical list.
func TestSortSharesIsFindingsFirst(t *testing.T) {
	list := []shares.Share{
		{Name: "aaa", Verdict: shares.VerdictOK},
		{Name: "homes", Special: true, Verdict: shares.VerdictOK},
		{Name: "zzz", Verdict: shares.VerdictRisk},
		{Name: "mmm", Verdict: shares.VerdictWarn},
	}
	shares.SortShares(list)

	var order []string
	for _, share := range list {
		order = append(order, share.Name)
	}
	want := "zzz,mmm,aaa,homes"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}
