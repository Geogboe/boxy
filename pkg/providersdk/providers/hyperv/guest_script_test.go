package hyperv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

type scriptGuestSession struct {
	countingGuestSession
	scripts   []string
	arguments [][]string
	scriptErr error
}

func (s *scriptGuestSession) ExecScript(_ context.Context, script string, args ...string) (*vmsdk.ExecResult, error) {
	s.scripts = append(s.scripts, script)
	s.arguments = append(s.arguments, append([]string(nil), args...))
	return &vmsdk.ExecResult{}, s.scriptErr
}

func TestPersonalizeDirectScriptsPreserveCredentialBoundary(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "network failure"}[fail], func(t *testing.T) {
			d := mockDriver(nil)
			d.resolveBootstrap = func(context.Context, string) (providersdk.GuestBootstrapCredential, error) {
				return providersdk.GuestBootstrapCredential{Username: "boxy-test-user", Password: "${BOXY_TEST_PASSWORD}"}, nil
			}
			d.psExec = func(_ context.Context, script string) (string, error) {
				switch {
				case strings.Contains(script, "Get-VMNetworkAdapter"):
					return "192.0.2.10", nil
				case strings.Contains(script, ").Name"):
					return "boxy-test-vm", nil
				default:
					return "boxy_guest_os=windows;boxy_net_static_ip=192.0.2.10;boxy_net_prefix=24", nil
				}
			}
			var sessions []*scriptGuestSession
			d.guestExecFactory = func(_, _, _, password, _ string) vmsdk.GuestExec {
				s := &scriptGuestSession{countingGuestSession: countingGuestSession{password: password}}
				if fail {
					s.scriptErr = errors.New("script failed")
				}
				if len(sessions) == 1 && sessions[0].closeCount != 1 {
					t.Fatal("verification opened before old session closed")
				}
				sessions = append(sessions, s)
				return s
			}
			_, err := d.PersonalizeGuest(context.Background(), fakeGUID, providersdk.GuestPersonalizationOptions{ApplyNetwork: true})
			if fail {
				if err == nil || len(sessions) != 1 || sessions[0].closeCount != 1 || len(sessions[0].scripts) != 1 {
					t.Fatal("network failure did not stop and close personalization")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 2 {
				t.Fatal("expected separate verification session")
			}
			old, fresh := sessions[0], sessions[1]
			if len(old.scripts) != 2 || old.execCount != 0 || fresh.execCount != 1 || len(fresh.scripts) != 0 {
				t.Fatal("incorrect script or verification path")
			}
			if old.closeCount != 1 || fresh.closeCount != 1 {
				t.Fatal("session not closed exactly once")
			}
			if fresh.password == old.password || fresh.password == "" || old.arguments[1][1] != fresh.password {
				t.Fatal("verification did not authenticate with rotated credential")
			}
			if strings.Contains(old.scripts[1], fresh.password) {
				t.Fatal("credential embedded in script source")
			}
			if old.arguments[0][0] != "192.0.2.10" {
				t.Fatal("address argument lost")
			}
		})
	}
}
