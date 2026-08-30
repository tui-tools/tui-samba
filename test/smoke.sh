#!/bin/bash
# Backend smoke test for tui-samba, run inside a lab guest.
#
# The contract (see tui-tools/tui-lab): this script runs on the guest as the
# unprivileged lab user, escalates with `sudo -n` only, prints a short PASS/FAIL
# table and exits non-zero if anything failed. The binary under test is at
# $TUI_LAB_BIN (default: tui-samba on PATH).
#
# What it proves is that the tool reads a *real* machine — not that a fake
# renders. The lab already covers --version and a --demo frame; this covers the
# backend.
#
# Most guests have no Samba at all, and that is the normal case rather than a
# gap: a machine without a file server is a real machine and "there is none
# here" is the true answer for it. So the assertions come in two parts. The
# first is that the absent path is *clean*: the read runs, names its backend,
# reports `"installed": false` with a reason, and still exits 0. The second, on
# a guest that does have Samba, is that the configuration is parsed — including
# the distribution's own default smb.conf, which is the one file every one of
# them ships.
#
# Everything the tool is asked to do is read-only. It is never asked to write a
# share, to change an account or to reload the server: a suite that rewrote
# /etc/samba/smb.conf would be a suite nobody could run twice.
set -uo pipefail

bin="${TUI_LAB_BIN:-tui-samba}"
# TOOL is the manifest name, which is what a compatibility result is keyed on.
TOOL=tui-samba
pass=0
fail=0

# check runs one assertion. It takes a label, a command and a grep pattern the
# command's output must match. Output is captured so a failure can show it.
check() {
  local label="$1" command="$2" pattern="$3" output status
  output=$(eval "$command" 2>&1)
  status=$?
  if [[ $status -eq 0 ]] && grep -qE "$pattern" <<<"$output"; then
    printf 'PASS  %s\n' "$label"
    pass=$((pass + 1))
  else
    printf 'FAIL  %s (exit %d)\n' "$label" "$status"
    sed 's/^/      | /' <<<"$output" | head -12
    fail=$((fail + 1))
  fi
}

# check_absent is the inverse of a grep assertion: the command must succeed and
# its output must NOT contain the pattern. It is what proves a read stayed a
# read, which is a claim about something that did not happen.
check_absent() {
  local label="$1" command="$2" pattern="$3" output status
  output=$(eval "$command" 2>&1)
  status=$?
  if [[ $status -eq 0 ]] && ! grep -qE "$pattern" <<<"$output"; then
    printf 'PASS  %s\n' "$label"
    pass=$((pass + 1))
  else
    printf 'FAIL  %s (exit %d)\n' "$label" "$status"
    sed 's/^/      | /' <<<"$output" | head -12
    fail=$((fail + 1))
  fi
}

# --- compatibility evidence -------------------------------------------------
#
# The manifest's `tested` list is generated, not claimed: it is rebuilt from
# compat/results.jsonl by tui-kit/tools/compat-sync.py, and this is where a
# line of that file comes from. The version recorded is the one the tool itself
# probed, read back out of --check, so it describes the machine that really ran
# the suite rather than what the tester assumed was installed.
#
# A guest with no Samba records nothing, which is not a failure: it is a
# machine with nothing to be compatible with.
#
# The line is printed behind a `compat-result:` prefix so it survives the trip
# out of the guest through the lab's per-VM log, and appended to
# $TUI_COMPAT_RESULTS as well for a run outside the lab.
record_compat() {
  local report="$1" outcome="$2" backend version distro today block
  block=$(sed -n '/"compat": {/,/^  }/p' <<<"$report")
  backend=$(sed -n 's/.*"backend": "\([^"]*\)".*/\1/p' <<<"$block" | head -1)
  version=$(sed -n 's/.*"version": "\([^"]*\)".*/\1/p' <<<"$block" | head -1)
  if [[ -z $backend || -z $version ]]; then
    echo "      no version was probed, so no compatibility result is recorded"
    return
  fi

  distro=$(. /etc/os-release && echo "${ID}-${VERSION_ID:-rolling}")
  today=$(date -u +%Y-%m-%d)
  local line
  line=$(printf '{"backend":"%s","date":"%s","distro":"%s","result":"%s","suite":"smoke","tool":"%s","version":"%s"}' \
    "$backend" "$today" "$distro" "$outcome" "$TOOL" "$version")

  printf 'compat-result: %s\n' "$line"
  if [[ -n ${TUI_COMPAT_RESULTS:-} ]]; then
    printf '%s\n' "$line" >>"$TUI_COMPAT_RESULTS"
  fi
}

echo "--- tui-samba smoke on $(. /etc/os-release && echo "$PRETTY_NAME")"

# What this machine has. smbd is in /usr/sbin on every distribution, so it is
# looked for by path as well as on PATH — which is the whole reason the
# manifest declares search paths for it.
if command -v smbd >/dev/null 2>&1 || [[ -x /usr/sbin/smbd ]]; then
  have_samba=yes
else
  have_samba=no
fi
for program in testparm pdbedit smbstatus smbcontrol smbclient; do
  if command -v "$program" >/dev/null 2>&1; then
    echo "      $program=yes"
  else
    echo "      $program=no"
  fi
done
echo "      smbd=$have_samba"
if sudo -n true 2>/dev/null; then
  privileged=yes
else
  privileged=no
fi
echo "      sudo -n=$privileged"

# --- the report block ------------------------------------------------------
#
# --report is read-only and unprivileged, so it is smoked without sudo: a user
# who cannot escalate is exactly the one who most needs to be able to file a
# usable bug. What is asserted is that it names the backend this tool drives,
# that it still answers under --demo, and that it keeps its privacy promise —
# the block goes into a public issue, so a home path or the host name appearing
# in it is a bug, not a cosmetic detail.
check "report names the backend" \
  "$bin --report" \
  '^backend: samba'

check "report says the run was live" \
  "$bin --report" \
  '^mode: live$'

check "report works in demo mode too" \
  "$bin --demo --report" \
  '^backend: demo$'

check "and says so on the mode line" \
  "$bin --demo --report" \
  '^mode: demo'

# The distro and kernel lines are excluded from the host-name search rather
# than from the promise: they are built from /etc/os-release and from uname's
# release and machine fields, never from its nodename, and on a guest called
# "fedora" or "ubuntu" — which is most of them — the host name is a substring
# of the distribution's own. Everything else in the block is searched.
check "report leaks neither a home path nor the host name" \
  "$bin --report | grep -vE '^(distro|kernel): ' | grep -cE '/home/|$(uname -n)' || true" \
  '^0$'

# 1. The read path works at all and names the backend it drove. This holds on
#    every machine, with or without Samba, and a non-zero exit here is the one
#    thing that is always a bug.
check "check reads the machine" \
  "$bin --check" \
  '"backend": "samba"'

# 2. It says whether there is a server, in a field a script can read.
check "installed is a boolean" \
  "$bin --check" \
  '"installed": (true|false)'

# 3. Every count is a number. A machine with no Samba reports 0, and 0 is an
#    answer: what would be a bug is a missing field or a word where a number
#    should be.
for field in shares writable guest pathMissing users sessions openFiles findings risks; do
  check "$field is an integer" "$bin --check" "\"$field\": [0-9]+"
done

# 4. The units are reported by the name they really have here. Fedora and Arch
#    ship smb.service, Debian and Ubuntu ship smbd.service, and a tool that
#    guessed would be wrong on half of them.
check "the file server unit is reported" \
  "$bin --check" \
  '"role": "file server"'

if [[ $have_samba == no ]]; then
  # --- the absent path, which is most machines ------------------------------
  #
  # This is the assertion that matters on a guest with no Samba: the read is
  # clean, it says why there is nothing, and it exits 0. A tool that treated a
  # machine without a file server as an error would fail on nearly every
  # machine in the lab.
  check "a machine with no Samba says so" \
    "$bin --check" \
    '"installed": false'

  check "and it says why" \
    "$bin --check" \
    '"detail": "no Samba server is installed'

  check "and it still exits 0" \
    "$bin --check >/dev/null && echo clean" \
    'clean'

  check "with no shares and no accounts" \
    "$bin --check" \
    '"shares": 0'
else
  # --- a machine that really has a file server -------------------------------
  #
  # Every distribution ships a default smb.conf, so there is always a
  # configuration to parse even on a server nobody has set up. What is asserted
  # is that it was parsed: the server's own version, the file it read, and the
  # dialect floor it resolved.
  check "the server version was read" \
    "$bin --check" \
    '"serverVersion": "[0-9]+\.[0-9]+'

  check "the configuration file is named" \
    "$bin --check" \
    '"configFile": "/.*smb\.conf"'

  # `security` is the one global every distribution's default sets, so it is
  # the safest thing to assert a parse on.
  check "the effective configuration was parsed" \
    "$bin --check" \
    '"security": "USER"'

  # The default smb.conf ships [homes] and [printers] on every distribution,
  # and both have to come back marked as Samba's own sections rather than as
  # directory exports somebody wrote.
  check "the default stanzas were read" \
    "$bin --check" \
    '"special": true'

  check "the dialect floor is reported" \
    "$bin --check" \
    '"smb1Enabled": (true|false)'

  # The accounts and the connections are root-only. With `sudo -n` they are
  # read; without it the tool has to say so rather than report an empty server.
  if [[ $privileged == yes ]]; then
    check "the password database was reachable" \
      "$bin --check" \
      '"users": [0-9]+'
  else
    check "an unprivileged run says why the accounts are missing" \
      "$bin --check" \
      '"usersDetail": ".*root'
  fi
fi

# 5. --check must never change anything. The one directory this tool would ever
#    create must not appear because of a read.
before=$(test -e /etc/samba/tui-samba.d && echo present || echo absent)
$bin --check >/dev/null 2>&1
after=$(test -e /etc/samba/tui-samba.d && echo present || echo absent)
if [[ "$before" == "$after" ]]; then
  printf 'PASS  --check left the machine untouched\n'
  pass=$((pass + 1))
else
  printf 'FAIL  --check created something (%s→%s)\n' "$before" "$after"
  fail=$((fail + 1))
fi

# 6. And it prints no mutation: --check reports the read path, and a command
#    line in its output would mean it had built one.
check_absent "--check builds no command" \
  "$bin --check" \
  'smbpasswd |install -m 644|reload-config|smbclient -L'

# 7. The one thing on this machine the tool must never touch is smb.conf
#    itself. A read that changed its modification time would be a read that
#    wrote.
if [[ -f /etc/samba/smb.conf ]]; then
  before_stamp=$(stat -c %Y /etc/samba/smb.conf 2>/dev/null)
  $bin --check >/dev/null 2>&1
  after_stamp=$(stat -c %Y /etc/samba/smb.conf 2>/dev/null)
  if [[ "$before_stamp" == "$after_stamp" ]]; then
    printf 'PASS  smb.conf was not written\n'
    pass=$((pass + 1))
  else
    printf 'FAIL  smb.conf changed during a read\n'
    fail=$((fail + 1))
  fi
fi

if [[ $fail -eq 0 ]]; then
  record_compat "$("$bin" --check 2>/dev/null)" pass
else
  record_compat "$("$bin" --check 2>/dev/null)" fail
fi

echo "--- tui-samba: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
