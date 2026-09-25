package agentserver

import (
	"context"
	"slices"
	"testing"
	"time"

	boxyagentv1 "github.com/Geogboe/boxy/pkg/agentproto/boxyagent/v1"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

// TestConnect_CarriesNetworkIsolatingProvidersFromRegistration covers C1's
// wire half: a remote agent's advertised isolation capability must survive
// registration into the AgentInfo the daemon later consults, and must be
// narrowed to the provider types the same frame actually registered -- the
// same anti-spoofing posture RemoteAgent.updateAvailability already applies
// to heartbeat availability entries.
func TestConnect_CarriesNetworkIsolatingProvidersFromRegistration(t *testing.T) {
	srv, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, testGoodToken, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: testGoodToken,
			AgentName:         "boxy-test-agent",
			ProviderTypes:     []string{"hyperv", "devfactory"},
			// "docker" is deliberately never registered above: an agent
			// must not be able to claim isolation for a provider it does
			// not serve.
			NetworkIsolatingProviderTypes: []string{"hyperv", "docker"},
			AgentVersion:                  testServerVersion,
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	resp := msg.GetRegistered()
	if resp == nil {
		t.Fatalf("expected a RegisterResponse, got %#v", msg)
	}

	agent, ok := srv.registry.Get(resp.GetAgentId())
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	info := agent.Info()
	if !slices.Equal(info.Providers, []providersdk.Type{"hyperv", "devfactory"}) {
		t.Fatalf("Providers = %v, want [hyperv devfactory]", info.Providers)
	}
	if !slices.Equal(info.NetworkIsolatingProviders, []providersdk.Type{"hyperv"}) {
		t.Fatalf("NetworkIsolatingProviders = %v, want [hyperv]; the unregistered \"docker\" claim must be dropped",
			info.NetworkIsolatingProviders)
	}
}

// A registration that advertises nothing must leave the field empty rather
// than defaulting to "every provider isolates" -- that default would
// reintroduce exactly the devfactory hard-fail C1 exists to prevent.
func TestConnect_NoIsolationClaimAdvertisesNone(t *testing.T) {
	srv, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, testGoodToken, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: testGoodToken,
			AgentName:         "boxy-test-agent",
			ProviderTypes:     []string{"devfactory"},
			AgentVersion:      testServerVersion,
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	resp := msg.GetRegistered()
	if resp == nil {
		t.Fatalf("expected a RegisterResponse, got %#v", msg)
	}

	agent, ok := srv.registry.Get(resp.GetAgentId())
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if got := agent.Info().NetworkIsolatingProviders; len(got) != 0 {
		t.Fatalf("NetworkIsolatingProviders = %v, want empty", got)
	}
}

// TestConnect_CarriesMeshCapableFromRegistration covers #379 blocker 6's
// wire half: an agent's probed mesh capability must survive registration
// into the AgentInfo the daemon later consults, exactly as-sent -- unlike
// NetworkIsolatingProviders, this is not narrowed against provider_types
// since it is not a per-provider claim (see AgentInfo.MeshCapable's doc
// comment).
func TestConnect_CarriesMeshCapableFromRegistration(t *testing.T) {
	srv, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, testGoodToken, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: testGoodToken,
			AgentName:         "boxy-test-agent",
			ProviderTypes:     []string{"hyperv"},
			AgentVersion:      testServerVersion,
			MeshCapable:       true,
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	resp := msg.GetRegistered()
	if resp == nil {
		t.Fatalf("expected a RegisterResponse, got %#v", msg)
	}

	agent, ok := srv.registry.Get(resp.GetAgentId())
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if !agent.Info().MeshCapable {
		t.Fatal("expected MeshCapable true, matching the RegisterRequest")
	}
}

// A registration that never sets mesh_capable must leave AgentInfo.MeshCapable
// false -- the correct reading for every agent built before #379 blocker 6
// (mesh_capable didn't exist on the wire) as well as one that simply hasn't
// enabled the overlay.
func TestConnect_NoMeshCapableClaimDefaultsFalse(t *testing.T) {
	srv, st, client, cleanup := newTestServer(t)
	defer cleanup()
	mintToken(t, st, testGoodToken, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := stream.Send(&boxyagentv1.AgentMessage{
		Payload: &boxyagentv1.AgentMessage_Register{Register: &boxyagentv1.RegisterRequest{
			RegistrationToken: testGoodToken,
			AgentName:         "boxy-test-agent",
			ProviderTypes:     []string{"devfactory"},
			AgentVersion:      testServerVersion,
		}},
	}); err != nil {
		t.Fatalf("send register request: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv register response: %v", err)
	}
	resp := msg.GetRegistered()
	if resp == nil {
		t.Fatalf("expected a RegisterResponse, got %#v", msg)
	}

	agent, ok := srv.registry.Get(resp.GetAgentId())
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if agent.Info().MeshCapable {
		t.Fatal("expected MeshCapable false when the registration never set it")
	}
}
