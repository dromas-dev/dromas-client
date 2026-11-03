//go:build windows

package client_test

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The tests here need what CI's windows-accounts job makes (.github/workflows/ci.yml,
// scripts/windows-accounts.ps1): local accounts and volumes with no access list. That
// job runs them under each account, named by accountEnv.
const accountEnv = "DROMAS_TEST_ACCOUNT"

func namedAccount(t *testing.T) string {
	t.Helper()
	a := os.Getenv(accountEnv)
	if a == "" {
		t.Skip("run by the windows-accounts job, under the accounts it makes")
	}
	return a
}

// The job's account is the one the process runs as, and none but SYSTEM holds the
// Administrators, so what a test of it passes is what that account can do.
func TestTheAccountIsTheOneNamed(t *testing.T) {
	name := namedAccount(t)
	t.Parallel()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid
	account, _, _, err := sid.LookupAccount("")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s runs as %s, %s", name, account, sid)
	var is bool
	switch name {
	case "plain", "other":
		is = strings.EqualFold(account, "dromas-"+name)
	case "system":
		is = sid.IsWellKnown(windows.WinLocalSystemSid)
	case "network-service":
		is = sid.IsWellKnown(windows.WinNetworkServiceSid)
	case "local-service":
		is = sid.IsWellKnown(windows.WinLocalServiceSid)
	case "guest":
		is = strings.HasSuffix(sid.String(), "-501")
	default:
		t.Fatalf("no account is named %s", name)
	}
	if !is {
		t.Errorf("the account named %s runs as %s, %s", name, account, sid)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	member, err := windows.Token(0).IsMember(admins)
	if err != nil {
		t.Fatal(err)
	}
	if member != (name == "system") {
		t.Errorf("%s: a member of the Administrators: %v", name, member)
	}
}
