package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tui-tools/tui-kit/compat"
	"github.com/tui-tools/tui-samba/internal/shares"
)

// checkTimeout bounds the read. Running testparm, pdbedit and smbstatus and
// stat'ing a handful of directories is fast; a machine whose share path is on
// a network file system that has gone away must not hang a non-interactive
// check forever.
const checkTimeout = 60 * time.Second

// shareReport is one share flattened into the fields a shell script can assert
// on without walking the model.
type shareReport struct {
	Name       string         `json:"name"`
	Path       string         `json:"path,omitempty"`
	ReadOnly   bool           `json:"readOnly"`
	GuestOK    bool           `json:"guestOk"`
	Browseable bool           `json:"browseable"`
	Special    bool           `json:"special"`
	Verdict    shares.Verdict `json:"verdict"`
	// PathExists and Mode are the Unix side, which is half of what goes wrong.
	PathExists bool   `json:"pathExists"`
	Mode       string `json:"mode,omitempty"`
	Owner      string `json:"owner,omitempty"`
	// ValidUsers is the access list, and Sessions how many clients have the
	// share open right now.
	ValidUsers []string `json:"validUsers,omitempty"`
	Sessions   int      `json:"sessions"`
	// Findings are the kinds only, so a script can grep for one without
	// matching a sentence that may be reworded.
	Findings []string `json:"findings,omitempty"`
}

// userReport is one Samba account, flattened the same way.
type userReport struct {
	Name        string `json:"name"`
	UID         int    `json:"uid"`
	Flags       string `json:"flags,omitempty"`
	Disabled    bool   `json:"disabled"`
	NoPassword  bool   `json:"noPassword"`
	UnixPresent bool   `json:"unixPresent"`
}

// serviceReport is one unit, flattened the same way.
type serviceReport struct {
	Unit    string `json:"unit"`
	Role    string `json:"role"`
	Present bool   `json:"present"`
	Enabled bool   `json:"enabled"`
	Active  bool   `json:"active"`
	State   string `json:"state,omitempty"`
}

// checkReport is what --check prints: whether there is a server at all, the
// counts, the shares and what is wrong with them, the accounts, who is
// connected, and the model in full.
//
// It is a report of the read path only. --check never builds and never runs a
// mutation: the whole point is that it is safe to run anywhere, including in
// CI against a production-shaped machine.
type checkReport struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Backend string `json:"backend"`
	// Describe is the backend's own one-line summary, which is where the demo
	// backend says it is a demo.
	Describe string `json:"describe"`

	// Installed is the first question, and a false one is a normal machine
	// rather than a failure: Detail says why.
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"`
	// ServerVersion is what smbd itself printed.
	ServerVersion string `json:"serverVersion,omitempty"`

	// The counts, which are what a smoke test asserts on before it asserts on
	// any particular share.
	Shares      int `json:"shares"`
	Writable    int `json:"writable"`
	Guest       int `json:"guest"`
	PathMissing int `json:"pathMissing"`
	Users       int `json:"users"`
	Disabled    int `json:"disabledUsers"`
	Sessions    int `json:"sessions"`
	OpenFiles   int `json:"openFiles"`
	Findings    int `json:"findings"`
	Risks       int `json:"risks"`

	// MinProtocol is the dialect floor, and SMB1 whether it lets a client
	// speak the protocol WannaCry travelled on.
	MinProtocol string `json:"minProtocol,omitempty"`
	SMB1        bool   `json:"smb1Enabled"`
	// Serving reports that a file server unit is actually running.
	Serving bool `json:"serving"`

	// ShareList is one row per stanza, in the order the screen shows them.
	ShareList []shareReport `json:"shareList"`
	// UserList is the Samba password database.
	UserList []userReport `json:"userList"`
	// Services is the units, and Ports what is listening.
	Services []serviceReport `json:"services"`
	Ports    []shares.Port   `json:"ports"`

	// GlobalFindings are the server-wide ones, by kind.
	GlobalFindings []string `json:"globalFindings,omitempty"`

	// Compat is what the version probe found. It is reported rather than
	// asserted: an untested version is a fact about the machine, not a failure
	// of the read path.
	Compat compat.Result `json:"compat"`
	// Model is the parsed state in full.
	Model shares.Model `json:"model"`
}

// runCheck exercises the backend's real read path and prints what it parsed as
// JSON. It returns an error when the backend cannot be read at all, which main
// turns into a non-zero exit — so a caller can treat the exit code alone as the
// verdict.
//
// A machine with no Samba on it is not a failure and never has been: most
// machines are like that, and `"installed": false` with the reason beside it is
// the true answer for them. What would be a failure is a backend that could not
// run its read, and that is what the error is for.
func runCheck(backend shares.Backend, backendCompat compat.Result,
	out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	model, err := backend.Load(ctx)
	if err != nil {
		return fmt.Errorf("%s backend read failed: %w", backend.Name(), err)
	}

	counts := model.Count()
	report := checkReport{
		Tool:          toolName,
		Version:       version,
		Backend:       backend.Name(),
		Describe:      backend.Describe(),
		Installed:     model.Installed,
		Detail:        model.Detail,
		ServerVersion: model.Version,
		Shares:        counts.Shares,
		Writable:      counts.Writable,
		Guest:         counts.Guest,
		PathMissing:   counts.PathMissing,
		Users:         counts.Users,
		Disabled:      counts.Disabled,
		Sessions:      counts.Sessions,
		OpenFiles:     counts.OpenFiles,
		Findings:      counts.Findings,
		Risks:         counts.Risks,
		MinProtocol:   model.Global.MinProtocol,
		SMB1:          model.Global.SMB1Enabled,
		Serving:       model.Serving(),
		Ports:         model.Ports,
		Compat:        backendCompat,
		Model:         model,
	}

	report.ShareList = make([]shareReport, 0, len(model.Shares))
	for _, share := range model.Shares {
		row := shareReport{
			Name:       share.Name,
			Path:       share.Path,
			ReadOnly:   share.ReadOnly,
			GuestOK:    share.GuestOK,
			Browseable: share.Browseable,
			Special:    share.Special,
			Verdict:    share.Verdict,
			PathExists: share.Dir.Exists,
			Mode:       share.Dir.Mode,
			Owner:      share.Dir.Owner,
			ValidUsers: share.ValidUsers,
			Sessions:   model.SessionsOn(share.Name),
		}
		for _, finding := range share.Findings {
			row.Findings = append(row.Findings, finding.Kind)
		}
		report.ShareList = append(report.ShareList, row)
	}

	report.UserList = make([]userReport, 0, len(model.Users))
	for _, user := range model.Users {
		report.UserList = append(report.UserList, userReport{
			Name:        user.Name,
			UID:         user.UID,
			Flags:       user.Flags,
			Disabled:    user.Disabled,
			NoPassword:  user.NoPassword,
			UnixPresent: user.UnixPresent,
		})
	}

	report.Services = make([]serviceReport, 0, len(model.Services))
	for _, service := range model.Services {
		report.Services = append(report.Services, serviceReport{
			Unit:    service.Unit,
			Role:    service.Role,
			Present: service.Present,
			Enabled: service.Enabled,
			Active:  service.Active,
			State:   service.State,
		})
	}

	for _, finding := range model.Global.Findings {
		report.GlobalFindings = append(report.GlobalFindings, finding.Kind)
	}

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
