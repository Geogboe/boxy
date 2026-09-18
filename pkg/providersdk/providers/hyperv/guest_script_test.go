package hyperv

import (
	"context"
	"encoding/json"
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
	failAt    int
}

func (s *scriptGuestSession) ExecScript(_ context.Context, script string, args ...string) (*vmsdk.ExecResult, error) {
	s.scripts = append(s.scripts, script)
	s.arguments = append(s.arguments, append([]string(nil), args...))
	if len(s.scripts) == s.failAt {
		return nil, s.scriptErr
	}
	return &vmsdk.ExecResult{}, nil
}

// TestPersonalizeDirectScriptsUseOneSession pins rotateGuestCredential's
// ExecScript path: rotation runs as one direct script call (structured
// arguments, no credential embedded in script source) on the pre-rotation
// session, which is closed before a separate session verifies the newly
// rotated credential (see TestDriver_PersonalizeGuest_OpensOneSessionPerCredential
// for why verification is never merged into the rotation session).
func TestPersonalizeDirectScriptsUseOneSession(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "rotation script failure"
		}
		t.Run(name, func(t *testing.T) {
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
					return "boxy_guest_os=windows;boxy_guest_user=boxy-test-user", nil
				}
			}
			var sessions []*scriptGuestSession
			d.guestExecFactory = func(_, _, _, password, _ string) vmsdk.GuestExec {
				s := &scriptGuestSession{countingGuestSession: countingGuestSession{password: password}}
				if fail {
					s.scriptErr = errors.New("script failed")
					s.failAt = 1
				}
				sessions = append(sessions, s)
				return s
			}
			result, err := d.PersonalizeGuest(context.Background(), fakeGUID, providersdk.GuestPersonalizationOptions{ApplyNetwork: true})
			if fail {
				if err == nil || result != nil || len(sessions) != 1 || sessions[0].closeCount != 1 || len(sessions[0].scripts) != 1 {
					t.Fatal("script failure did not stop and close personalization")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 2 {
				t.Fatal("expected the rotation session and a separate verification session")
			}
			old := sessions[0]
			if len(old.scripts) != 1 || old.execCount != 0 {
				t.Fatal("incorrect direct script path")
			}
			if old.closeCount != 1 {
				t.Fatal("session not closed exactly once")
			}
			var credential struct {
				Password string `json:"password"`
			}
			if err := json.Unmarshal(result.EphemeralCredential.Data, &credential); err != nil {
				t.Fatal(err)
			}
			if credential.Password == old.password || credential.Password == "" || old.arguments[0][1] != credential.Password {
				t.Fatal("returned credential does not match rotation argument")
			}
			if strings.Contains(old.scripts[0], credential.Password) {
				t.Fatal("credential embedded in script source")
			}
			if sessions[1].password != credential.Password {
				t.Fatal("verification session did not use the rotated credential")
			}
		})
	}
}
