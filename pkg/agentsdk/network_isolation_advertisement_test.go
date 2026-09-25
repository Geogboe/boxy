package agentsdk

import (
	"context"
	"slices"
	"testing"
	"time"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// These cover C1's capability advertisement: AgentInfo.NetworkIsolatingProviders
// must reflect which of an agent's own drivers really implement
// providersdk.NetworkIsolator, for both agent implementations, since the
// NetworkIsolatingAgent interface itself is satisfied unconditionally by
// both and so proves nothing about the driver behind it.

func TestNewEmbeddedAgent_AdvertisesOnlyIsolatingDrivers(t *testing.T) {
	isolating := &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}, createSegmentRef: "boxy-sb-sb-1"}
	plain := &fakeDriver{providerType: "devfactory"}

	agent, err := NewEmbeddedAgent("agent-1", "agent-1", false, isolating, plain)
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}

	info := agent.Info()
	if !slices.Equal(info.Providers, []providersdk.Type{"hyperv", "devfactory"}) {
		t.Fatalf("Providers = %v, want [hyperv devfactory]", info.Providers)
	}
	if !slices.Equal(info.NetworkIsolatingProviders, []providersdk.Type{"hyperv"}) {
		t.Fatalf("NetworkIsolatingProviders = %v, want [hyperv] only", info.NetworkIsolatingProviders)
	}
}

func TestNewEmbeddedAgent_AdvertisesNoneWhenNoDriverIsolates(t *testing.T) {
	agent, err := NewEmbeddedAgent("agent-1", "agent-1", false, &fakeDriver{providerType: "devfactory"})
	if err != nil {
		t.Fatalf("NewEmbeddedAgent: %v", err)
	}
	if got := agent.Info().NetworkIsolatingProviders; len(got) != 0 {
		t.Fatalf("NetworkIsolatingProviders = %v, want empty for a devfactory-only agent", got)
	}
}

func TestNetworkIsolatingProviderTypes_OrderFollowsProvidersNotMapIteration(t *testing.T) {
	drivers := DriverSet{
		"hyperv": &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}},
		"docker": &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "docker"}},
		"devfac": &fakeDriver{providerType: "devfac"},
	}
	// Repeated because Go randomizes map iteration order: a single pass
	// could coincidentally match even if the implementation ranged the map.
	for i := range 20 {
		got := NetworkIsolatingProviderTypes(drivers, []providersdk.Type{"docker", "devfac", "hyperv"})
		if !slices.Equal(got, []providersdk.Type{"docker", "hyperv"}) {
			t.Fatalf("iteration %d: got %v, want [docker hyperv] in the providers slice's own order", i, got)
		}
	}
}

func TestNetworkIsolatingProviderTypes_SkipsProviderWithNoDriver(t *testing.T) {
	drivers := DriverSet{"hyperv": &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}}}
	got := NetworkIsolatingProviderTypes(drivers, []providersdk.Type{"hyperv", "ghost"})
	if !slices.Equal(got, []providersdk.Type{"hyperv"}) {
		t.Fatalf("got %v, want [hyperv]; a provider with no driver entry must not be advertised", got)
	}
}

// TestRunSession_RegisterAdvertisesNetworkIsolatingProviders is the remote
// half: the daemon cannot type-assert a driver on another host, so the agent
// process must report the answer in its registration frame.
func TestRunSession_RegisterAdvertisesNetworkIsolatingProviders(t *testing.T) {
	stream := newFakeClientStream()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = RunSession(ctx, stream, RemoteClientConfig{
			AgentName:     "agent-1",
			AgentVersion:  "v-test",
			ProviderTypes: []providersdk.Type{"hyperv", "devfactory"},
			Drivers: DriverSet{
				"hyperv":     &fakeIsolatingDriver{fakeDriver: &fakeDriver{providerType: "hyperv"}},
				"devfactory": &fakeDriver{providerType: "devfactory"},
			},
			HeartbeatInterval: time.Hour,
		})
	}()

	var sent *boxyagentv1.AgentMessage
	select {
	case sent = <-stream.sentCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RegisterRequest")
	}
	cancel()
	stream.close()
	<-done

	reg := sent.GetRegister()
	if reg == nil {
		t.Fatalf("first frame was not a RegisterRequest: %#v", sent)
	}
	if !slices.Equal(reg.GetProviderTypes(), []string{"hyperv", "devfactory"}) {
		t.Fatalf("provider_types = %v, want [hyperv devfactory]", reg.GetProviderTypes())
	}
	if !slices.Equal(reg.GetNetworkIsolatingProviderTypes(), []string{"hyperv"}) {
		t.Fatalf("network_isolating_provider_types = %v, want [hyperv] only",
			reg.GetNetworkIsolatingProviderTypes())
	}
}
