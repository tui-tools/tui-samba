package samba

import (
	"sort"
	"strconv"
	"strings"

	"github.com/tui-tools/tui-samba/internal/shares"
)

// This file holds the opinions. Everything here is a pure function of the
// parsed model, so what the screen says about a share can be tested without a
// Samba anywhere near it.
//
// The bar for a finding is deliberately high. A list that flags what everybody
// runs teaches people to stop reading the list, so a read-only share exported
// to named users raises nothing at all, and neither does [homes] — which is on
// by default on every distribution and is not a mistake.

// JudgeShare works out what is worth saying about one share, and the verdict
// that sorts it.
func JudgeShare(share shares.Share, global shares.Global) shares.Share {
	share.Findings = nil

	// A path that is not there is the first thing to say, because every client
	// sees it as a permission error and goes looking in the wrong place.
	if !share.Special && share.Path != "" && !share.Dir.Exists {
		share.Findings = append(share.Findings, shares.Finding{
			Kind:    shares.FindingPathMissing,
			Verdict: shares.VerdictRisk,
			Message: share.Path + " does not exist" + reasonSuffix(share.Dir.Note) +
				", so every client is refused with an error that says nothing " +
				"about a missing directory",
		})
	}

	// A directory anybody on the machine can write to is a fact no Samba
	// setting takes back: `read only = yes` stops the share, not the shell.
	if share.Dir.WorldWritable {
		share.Findings = append(share.Findings, shares.Finding{
			Kind:    shares.FindingWorldWritable,
			Verdict: shares.VerdictRisk,
			Message: share.Path + " is mode " + share.Dir.Mode +
				": every account on this machine can write to it, whatever " +
				"the share says",
		})
	}

	// Writable and open to anyone is the combination that turns a file server
	// into a drop box for whoever reaches the port.
	if share.GuestOK && share.Writable() {
		share.Findings = append(share.Findings, shares.Finding{
			Kind:    shares.FindingGuestWritable,
			Verdict: shares.VerdictRisk,
			Message: "anyone who can reach this server can write here: " +
				"`guest ok = yes` with `read only = no` asks for no password",
		})
	}

	// Writable with no list at all means every account in the Samba database,
	// which is rarely what somebody setting up a team share meant.
	if share.Writable() && !share.GuestOK && !share.Special &&
		len(share.ValidUsers) == 0 && len(share.WriteList) == 0 {
		share.Findings = append(share.Findings, shares.Finding{
			Kind:    shares.FindingNoAccessControl,
			Verdict: shares.VerdictWarn,
			Message: "every account in the Samba database can write here — " +
				"there is no `valid users` and no `write list`",
		})
	}

	// A guest share on a server that maps unknown logins to the guest is the
	// case where a typo in a username gets in anyway.
	if share.GuestOK && badUserMapping(global) {
		share.Findings = append(share.Findings, shares.Finding{
			Kind:    shares.FindingMapToGuest,
			Verdict: shares.VerdictWarn,
			Message: "`map to guest = " + global.MapToGuest + "` means a login " +
				"this server does not know becomes the guest here instead of " +
				"being refused",
		})
	}

	share.Verdict = worstOf(share.Findings)
	if share.Verdict == shares.VerdictNone {
		share.Verdict = shares.VerdictOK
	}
	return share
}

// reasonSuffix appends the reason a stat could not be made, when there is one.
func reasonSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}

// badUserMapping reports the `map to guest` settings that let an unknown login
// through. "Never" is the default and refuses; "Bad Password" is worse still
// and is treated the same as "Bad User" here.
func badUserMapping(global shares.Global) bool {
	value := strings.ToLower(strings.TrimSpace(global.MapToGuest))
	return value == "bad user" || value == "bad password" || value == "bad uid"
}

// JudgeGlobal works out what is worth saying about the server itself.
func JudgeGlobal(global shares.Global, list []shares.Share) []shares.Finding {
	var findings []shares.Finding

	if global.SMB1Enabled {
		findings = append(findings, shares.Finding{
			Kind:    shares.FindingSMB1,
			Verdict: shares.VerdictRisk,
			Message: "`server min protocol = " + global.MinProtocol +
				"` lets a client speak SMB1, the dialect WannaCry travelled on. " +
				"Samba has defaulted to SMB2 since 4.11, so this was set on purpose",
		})
	}

	// `security = share` was removed in Samba 4.0 and is still copied out of
	// tutorials. A server carrying it is a server whose smb.conf has not been
	// read since then.
	if strings.EqualFold(global.Params["security"], "share") {
		findings = append(findings, shares.Finding{
			Kind:    shares.FindingSecurityShare,
			Verdict: shares.VerdictRisk,
			Message: "`security = share` was removed in Samba 4.0 and does " +
				"nothing here; whatever this configuration was meant to do, it " +
				"is not doing it",
		})
	}

	if badUserMapping(global) && anyGuest(list) {
		findings = append(findings, shares.Finding{
			Kind:    shares.FindingMapToGuest,
			Verdict: shares.VerdictWarn,
			Message: "`map to guest = " + global.MapToGuest + "` with " +
				plural(countGuest(list), "guest share") +
				": an unknown username becomes the guest instead of being refused",
		})
	}
	return findings
}

// anyGuest reports whether any share accepts a connection with no password.
func anyGuest(list []shares.Share) bool { return countGuest(list) > 0 }

// countGuest is how many shares accept one.
func countGuest(list []shares.Share) int {
	count := 0
	for _, share := range list {
		if share.GuestOK {
			count++
		}
	}
	return count
}

// plural renders a count with its noun, which a sentence needs and Go does
// not have.
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}

// worstOf is the verdict of the worst finding in a list.
func worstOf(findings []shares.Finding) shares.Verdict {
	verdict := shares.VerdictNone
	for _, finding := range findings {
		switch finding.Verdict {
		case shares.VerdictRisk:
			return shares.VerdictRisk
		case shares.VerdictWarn:
			verdict = shares.VerdictWarn
		}
	}
	return verdict
}

// JudgeUser adds the one sentence an account may deserve.
//
// There is only one, and it is about the Unix side: Samba's database maps a
// name to a Unix account, and an entry whose Unix account is gone is an entry
// nobody can ever log in as. Samba itself says nothing about it.
func JudgeUser(user shares.User) shares.User {
	switch {
	case !user.UnixPresent:
		user.Note = "there is no Unix account called " + user.Name +
			", so this entry cannot be logged in as; tui-users is where a Unix " +
			"account is created"
	case user.NoPassword:
		user.Note = "this account is flagged as needing no password"
	case user.Disabled:
		user.Note = "disabled: the password is still here, and no login works"
	}
	return user
}

// sortSessions orders sessions by the account, then by the machine, so two
// reads of an unchanged server produce the same screen.
func sortSessions(list []shares.Session) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].User != list[j].User {
			return list[i].User < list[j].User
		}
		if list[i].Machine != list[j].Machine {
			return list[i].Machine < list[j].Machine
		}
		return list[i].PID < list[j].PID
	})
}

// sortConnections orders tree connects by share, then by machine.
func sortConnections(list []shares.TreeConnect) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Service != list[j].Service {
			return list[i].Service < list[j].Service
		}
		return list[i].Machine < list[j].Machine
	})
}

// sortFiles orders open files by path, which is the column a reader scans.
func sortFiles(list []shares.OpenFile) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Path != list[j].Path {
			return list[i].Path < list[j].Path
		}
		return list[i].PID < list[j].PID
	})
}
