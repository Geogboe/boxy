package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	systemtypes "github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
)

var _ providersdk.AvailabilityReporter = (*Driver)(nil)

func TestDriver_AvailabilityReportsDockerHostMemoryEstimate(t *testing.T) {
	called := false
	d := &Driver{cli: &mockDockerClient{info: func(context.Context) (systemtypes.Info, error) {
		called = true
		return systemtypes.Info{MemTotal: 8 * 1024 * 1024 * 1024}, nil
	}}}

	sample, err := d.Availability(context.Background())
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if !called {
		t.Fatal("Docker Info was not called")
	}
	if sample == nil || sample.MemoryMB != 8192 {
		t.Fatalf("sample = %+v, want 8192 MiB", sample)
	}
}

func TestDriver_AvailabilityPropagatesDockerInfoError(t *testing.T) {
	want := fmt.Errorf("engine unavailable")
	d := &Driver{cli: &mockDockerClient{info: func(context.Context) (systemtypes.Info, error) {
		return systemtypes.Info{}, want
	}}}

	if _, err := d.Availability(context.Background()); err == nil || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("Availability error = %v, want wrapped Docker Info error", err)
	}
}

func TestDockerScriptCacheUsesGuestSpecificPathsAndLimits(t *testing.T) {
	linuxPath := dockerScriptPath("ABCDEF", providersdk.ScriptInterpreterSH)
	if linuxPath != "/tmp/boxy-script-cache/abcdef.sh" {
		t.Fatalf("Linux script path = %q", linuxPath)
	}

	powerShellPath := dockerScriptPath("ABCDEF", providersdk.ScriptInterpreterPowerShell)
	if powerShellPath != `C:\Windows\Temp\boxy-script-cache\abcdef.ps1` {
		t.Fatalf("Windows script path = %q", powerShellPath)
	}

	powershellStage := dockerPowerShellStageCommand(powerShellPath)
	for _, expected := range []string{`C:\Windows\Temp\boxy-script-cache\`, "Move-Item", "64", "33554432"} {
		if !strings.Contains(powershellStage, expected) {
			t.Errorf("PowerShell staging command missing %q: %s", expected, powershellStage)
		}
	}

	shellStage := dockerShellStageCommand(linuxPath)
	for _, expected := range []string{"/tmp/boxy-script-cache", "max_files=64", "max_bytes=33554432"} {
		if !strings.Contains(shellStage, expected) {
			t.Errorf("shell staging command missing %q: %s", expected, shellStage)
		}
	}
}

func TestDockerScriptInterpreterRejectsPlatformMismatches(t *testing.T) {
	tests := []struct {
		name      string
		requested providersdk.ScriptInterpreter
		platform  string
		image     string
		want      providersdk.ScriptInterpreter
		wantErr   string
	}{
		{name: "auto linux", requested: providersdk.ScriptInterpreterAuto, platform: "linux", want: providersdk.ScriptInterpreterSH},
		{name: "auto windows", requested: providersdk.ScriptInterpreterAuto, platform: "windows", want: providersdk.ScriptInterpreterPowerShell},
		{name: "explicit sh linux", requested: providersdk.ScriptInterpreterSH, platform: "linux", want: providersdk.ScriptInterpreterSH},
		{name: "explicit powershell windows", requested: providersdk.ScriptInterpreterPowerShell, platform: "windows", want: providersdk.ScriptInterpreterPowerShell},
		{name: "powershell on linux", requested: providersdk.ScriptInterpreterPowerShell, platform: "linux", wantErr: "require a Windows Docker container"},
		{name: "sh on windows", requested: providersdk.ScriptInterpreterSH, platform: "windows", wantErr: "require a Linux Docker container"},
		{name: "image windows hint", requested: providersdk.ScriptInterpreterSH, image: "mcr.microsoft.com/windows/servercore:ltsc2022", wantErr: "require a Linux Docker container"},
		{name: "ambiguous auto", requested: providersdk.ScriptInterpreterAuto, image: "custom-image", wantErr: "specify --interpreter"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := dockerScriptInterpreter(test.requested, test.platform, test.image)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("dockerScriptInterpreter: %v", err)
			}
			if got != test.want {
				t.Fatalf("interpreter = %q, want %q", got, test.want)
			}
		})
	}
}

// mockDockerClient is a test double for dockerClient.
type mockDockerClient struct {
	imageInspect         func(ctx context.Context, imageID string, inspectOpts ...client.ImageInspectOption) (imagetypes.InspectResponse, error)
	imagePull            func(ctx context.Context, refStr string, options imagetypes.PullOptions) (io.ReadCloser, error)
	containerCreate      func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	containerStart       func(ctx context.Context, containerID string, options container.StartOptions) error
	containerInspect     func(ctx context.Context, containerID string) (container.InspectResponse, error)
	containerList        func(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	containerLogs        func(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	containerExecCreate  func(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error)
	containerExecAttach  func(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error)
	containerExecInspect func(ctx context.Context, execID string) (container.ExecInspect, error)
	containerRemove      func(ctx context.Context, containerID string, options container.RemoveOptions) error
	info                 func(ctx context.Context) (systemtypes.Info, error)
}

func (m *mockDockerClient) ImageInspect(ctx context.Context, imageID string, opts ...client.ImageInspectOption) (imagetypes.InspectResponse, error) {
	if m.imageInspect != nil {
		return m.imageInspect(ctx, imageID, opts...)
	}
	return imagetypes.InspectResponse{}, nil
}
func (m *mockDockerClient) ImagePull(ctx context.Context, ref string, opts imagetypes.PullOptions) (io.ReadCloser, error) {
	if m.imagePull != nil {
		return m.imagePull(ctx, ref, opts)
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (m *mockDockerClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	return m.containerCreate(ctx, config, hostConfig, networkingConfig, platform, containerName)
}
func (m *mockDockerClient) ContainerStart(ctx context.Context, id string, opts container.StartOptions) error {
	return m.containerStart(ctx, id, opts)
}
func (m *mockDockerClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	return m.containerInspect(ctx, id)
}
func (m *mockDockerClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
	if m.containerList != nil {
		return m.containerList(ctx, opts)
	}
	return nil, nil
}
func (m *mockDockerClient) ContainerLogs(ctx context.Context, id string, opts container.LogsOptions) (io.ReadCloser, error) {
	if m.containerLogs != nil {
		return m.containerLogs(ctx, id, opts)
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (m *mockDockerClient) ContainerExecCreate(ctx context.Context, id string, opts container.ExecOptions) (container.ExecCreateResponse, error) {
	return m.containerExecCreate(ctx, id, opts)
}
func (m *mockDockerClient) ContainerExecAttach(ctx context.Context, id string, cfg container.ExecAttachOptions) (types.HijackedResponse, error) {
	return m.containerExecAttach(ctx, id, cfg)
}
func (m *mockDockerClient) ContainerExecInspect(ctx context.Context, id string) (container.ExecInspect, error) {
	return m.containerExecInspect(ctx, id)
}
func (m *mockDockerClient) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	return m.containerRemove(ctx, id, opts)
}
func (m *mockDockerClient) Info(ctx context.Context) (systemtypes.Info, error) {
	if m.info != nil {
		return m.info(ctx)
	}
	return systemtypes.Info{}, nil
}

// runningInspect returns an InspectResponse that looks like a running container.
func runningInspect(id, name string) container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    id,
			Name:  "/" + name,
			State: &container.State{Running: true, Status: "running"},
		},
	}
}

func TestDriver_UpdateStreamEmitsLiveOutput(t *testing.T) {
	mock := &mockDockerClient{
		containerExecCreate: func(_ context.Context, id string, opts container.ExecOptions) (container.ExecCreateResponse, error) {
			if id != "container-1" || len(opts.Cmd) != 1 || opts.Cmd[0] != "hostname" {
				t.Fatalf("exec create = id %q opts %+v", id, opts)
			}
			return container.ExecCreateResponse{ID: "exec-1"}, nil
		},
		containerExecAttach: func(_ context.Context, id string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
			if id != "exec-1" {
				t.Fatalf("exec attach id = %q, want exec-1", id)
			}
			return pipeHijack(stdcopyFrame("hello\n")), nil
		},
		containerExecInspect: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{ExitCode: 0}, nil
		},
	}
	d := &Driver{cli: mock}
	sink := &recordingEventSink{}
	result, err := d.UpdateStream(context.Background(), "container-1", &ExecOp{Command: []string{"hostname"}}, sink)
	if err != nil {
		t.Fatalf("UpdateStream: %v", err)
	}
	if result.Outputs["exit_code"] != "0" {
		t.Fatalf("exit_code = %q, want 0", result.Outputs["exit_code"])
	}
	if len(sink.events) != 1 || string(sink.events[0].Payload) != "hello\n" || sink.events[0].Channel != eventstream.Channel("stdout") {
		t.Fatalf("events = %+v, want stdout hello newline", sink.events)
	}
}

type recordingEventSink struct {
	events []eventstream.Event
}

func (s *recordingEventSink) Send(_ context.Context, event eventstream.Event) error {
	s.events = append(s.events, event)
	return nil
}

// stdcopyFrame builds a Docker multiplexed stream frame (stdout).}]}_ }
// Format: [stream_type(1), 0,0,0, size(4-byte big-endian), data...]
func stdcopyFrame(stdout string) []byte {
	data := []byte(stdout)
	var buf bytes.Buffer
	buf.WriteByte(1) // stdout
	buf.Write([]byte{0, 0, 0})
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(data)))
	buf.Write(size)
	buf.Write(data)
	return buf.Bytes()
}

// pipeHijack returns a types.HijackedResponse backed by a bytes reader.
func pipeHijack(data []byte) types.HijackedResponse {
	conn1, conn2 := net.Pipe()
	go func() {
		conn2.Write(data) //nolint:errcheck
		conn2.Close()     //nolint:errcheck
	}()
	return types.HijackedResponse{
		Conn:   conn1,
		Reader: bufio.NewReader(conn1),
	}
}

type notFoundError struct{ msg string }

func (e notFoundError) Error() string { return e.msg }
func (notFoundError) NotFound()       {}

type trackingReadCloser struct {
	reader io.Reader
	closed bool
}

func (r *trackingReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

// --- Create ---

func TestDriver_Create_HappyPath(t *testing.T) {
	const containerID = "abc123def456"
	mock := &mockDockerClient{
		containerCreate: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
			if !strings.HasPrefix(name, "boxy-") {
				t.Errorf("container name %q should start with boxy-", name)
			}
			return container.CreateResponse{ID: containerID}, nil
		},
		containerStart: func(_ context.Context, id string, _ container.StartOptions) error {
			if id != containerID {
				t.Errorf("start called with %q, want %q", id, containerID)
			}
			return nil
		},
		containerInspect: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return runningInspect(containerID, "boxy-abc123"), nil
		},
	}

	d := &Driver{cli: mock}
	res, err := d.Create(context.Background(), &CreateConfig{Image: "alpine:latest"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ID != containerID {
		t.Errorf("ID = %q, want %q", res.ID, containerID)
	}
	if res.ConnectionInfo["image"] != "alpine:latest" {
		t.Errorf("image = %q, want alpine:latest", res.ConnectionInfo["image"])
	}
}

func TestDriver_Create_PullsMissingImage(t *testing.T) {
	const imageRef = "alpine:latest"

	inspectCalls := 0
	pullCalls := 0
	createCalls := 0
	stream := &trackingReadCloser{
		reader: strings.NewReader("{\"status\":\"Pulled\"}\n"),
	}
	mock := &mockDockerClient{
		imageInspect: func(_ context.Context, got string, _ ...client.ImageInspectOption) (imagetypes.InspectResponse, error) {
			inspectCalls++
			if got != imageRef {
				t.Errorf("inspect image = %q, want %q", got, imageRef)
			}
			return imagetypes.InspectResponse{}, notFoundError{msg: "missing image"}
		},
		imagePull: func(_ context.Context, got string, _ imagetypes.PullOptions) (io.ReadCloser, error) {
			pullCalls++
			if got != imageRef {
				t.Errorf("pull image = %q, want %q", got, imageRef)
			}
			return stream, nil
		},
		containerCreate: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
			createCalls++
			return container.CreateResponse{ID: "abc123def456"}, nil
		},
		containerStart: func(_ context.Context, _ string, _ container.StartOptions) error { return nil },
		containerInspect: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return runningInspect("abc123def456", "boxy-abc123"), nil
		},
	}

	d := &Driver{cli: mock}
	if _, err := d.Create(context.Background(), &CreateConfig{Image: imageRef}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inspectCalls != 1 {
		t.Fatalf("inspect calls = %d, want 1", inspectCalls)
	}
	if pullCalls != 1 {
		t.Fatalf("pull calls = %d, want 1", pullCalls)
	}
	if createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", createCalls)
	}
	if !stream.closed {
		t.Fatal("expected image pull stream to be closed")
	}
}

func TestDriver_Create_ImageInspectError(t *testing.T) {
	mock := &mockDockerClient{
		imageInspect: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (imagetypes.InspectResponse, error) {
			return imagetypes.InspectResponse{}, fmt.Errorf("docker daemon unavailable")
		},
	}

	d := &Driver{cli: mock}
	_, err := d.Create(context.Background(), &CreateConfig{Image: "alpine:latest"})
	if err == nil {
		t.Fatal("expected inspect error")
	}
	if !strings.Contains(err.Error(), `docker ImageInspect "alpine:latest"`) {
		t.Fatalf("error = %q, want image inspect context", err)
	}
}

func TestDriver_Create_ImagePullError(t *testing.T) {
	mock := &mockDockerClient{
		imageInspect: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (imagetypes.InspectResponse, error) {
			return imagetypes.InspectResponse{}, notFoundError{msg: "missing image"}
		},
		imagePull: func(_ context.Context, _ string, _ imagetypes.PullOptions) (io.ReadCloser, error) {
			return nil, fmt.Errorf("registry timeout")
		},
	}

	d := &Driver{cli: mock}
	_, err := d.Create(context.Background(), &CreateConfig{Image: "alpine:latest"})
	if err == nil {
		t.Fatal("expected pull error")
	}
	if !strings.Contains(err.Error(), `docker ImagePull "alpine:latest": registry timeout`) {
		t.Fatalf("error = %q, want image pull context", err)
	}
}

func TestDriver_Create_ImagePullStreamError(t *testing.T) {
	stream := &trackingReadCloser{
		reader: strings.NewReader("{\"errorDetail\":{\"message\":\"pull failed\"},\"error\":\"pull failed\"}\n"),
	}
	mock := &mockDockerClient{
		imageInspect: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (imagetypes.InspectResponse, error) {
			return imagetypes.InspectResponse{}, notFoundError{msg: "missing image"}
		},
		imagePull: func(_ context.Context, _ string, _ imagetypes.PullOptions) (io.ReadCloser, error) {
			return stream, nil
		},
	}

	d := &Driver{cli: mock}
	_, err := d.Create(context.Background(), &CreateConfig{Image: "alpine:latest"})
	if err == nil {
		t.Fatal("expected pull stream error")
	}
	if !strings.Contains(err.Error(), `docker ImagePull "alpine:latest": pull failed`) {
		t.Fatalf("error = %q, want JSON stream pull error", err)
	}
	if !stream.closed {
		t.Fatal("expected image pull stream to be closed")
	}
}

func TestDriver_Create_MissingImage(t *testing.T) {
	d := &Driver{cli: &mockDockerClient{}}
	_, err := d.Create(context.Background(), &CreateConfig{})
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("expected image error, got: %v", err)
	}
}

func TestDriver_Create_ContainerNotRunning(t *testing.T) {
	removed := false
	mock := &mockDockerClient{
		containerCreate: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "badcontainer"}, nil
		},
		containerStart: func(_ context.Context, _ string, _ container.StartOptions) error { return nil },
		containerInspect: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "badcontainer",
					Name:  "/boxy-bad",
					State: &container.State{Running: false, Status: "exited"},
				},
			}, nil
		},
		containerRemove: func(_ context.Context, _ string, _ container.RemoveOptions) error {
			removed = true
			return nil
		},
	}

	d := &Driver{cli: mock}
	_, err := d.Create(context.Background(), &CreateConfig{Image: "bad:image"})
	if err == nil {
		t.Fatal("expected error when container not running")
	}
	if !removed {
		t.Error("expected cleanup ContainerRemove to be called")
	}
}

// --- Read ---

func TestDriver_Read_State(t *testing.T) {
	mock := &mockDockerClient{
		containerInspect: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc",
					State: &container.State{Status: "running"},
				},
			}, nil
		},
	}
	d := &Driver{cli: mock}
	status, err := d.Read(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.State != "running" {
		t.Errorf("state = %q, want running", status.State)
	}
}

func TestDriver_Read_Error(t *testing.T) {
	mock := &mockDockerClient{
		containerInspect: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("no such container")
		},
	}
	d := &Driver{cli: mock}
	_, err := d.Read(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- List ---

func TestDriver_List_FiltersByManagedLabel(t *testing.T) {
	mock := &mockDockerClient{
		containerList: func(_ context.Context, opts container.ListOptions) ([]container.Summary, error) {
			if !opts.All {
				t.Errorf("expected All=true so stopped containers are included")
			}
			if opts.Filters.Len() != 1 {
				t.Fatalf("expected exactly one filter, got %d", opts.Filters.Len())
			}
			if !opts.Filters.ExactMatch("label", managedLabel+"="+managedLabelValue) {
				t.Errorf("expected label filter %s=%s", managedLabel, managedLabelValue)
			}
			return []container.Summary{
				{ID: "abc", State: "running"},
				{ID: "def", State: "exited"},
			}, nil
		},
	}
	d := &Driver{cli: mock}
	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses, want 2", len(statuses))
	}
	if statuses[0].ID != "abc" || statuses[0].State != "running" {
		t.Errorf("unexpected first status: %+v", statuses[0])
	}
	if statuses[1].ID != "def" || statuses[1].State != "exited" {
		t.Errorf("unexpected second status: %+v", statuses[1])
	}
}

func TestDriver_List_Empty(t *testing.T) {
	mock := &mockDockerClient{
		containerList: func(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
			return nil, nil
		},
	}
	d := &Driver{cli: mock}
	statuses, err := d.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("got %d statuses, want 0", len(statuses))
	}
}

func TestDriver_List_Error(t *testing.T) {
	mock := &mockDockerClient{
		containerList: func(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
			return nil, fmt.Errorf("docker daemon unreachable")
		},
	}
	d := &Driver{cli: mock}
	_, err := d.List(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Update ---

func TestDriver_Update_ExecOp(t *testing.T) {
	const execID = "exec123"
	mock := &mockDockerClient{
		containerExecCreate: func(_ context.Context, _ string, opts container.ExecOptions) (container.ExecCreateResponse, error) {
			if len(opts.Cmd) == 0 || opts.Cmd[0] != "echo" {
				t.Errorf("unexpected cmd: %v", opts.Cmd)
			}
			return container.ExecCreateResponse{ID: execID}, nil
		},
		containerExecAttach: func(_ context.Context, id string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
			if id != execID {
				t.Errorf("execID = %q, want %q", id, execID)
			}
			return pipeHijack(stdcopyFrame("hello\n")), nil
		},
		containerExecInspect: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{ExitCode: 0}, nil
		},
	}

	d := &Driver{cli: mock}
	result, err := d.Update(context.Background(), "container1", &ExecOp{Command: []string{"echo", "hello"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outputs["stdout"] != "hello\n" {
		t.Errorf("stdout = %q, want %q", result.Outputs["stdout"], "hello\n")
	}
	if result.Outputs["exit_code"] != "0" {
		t.Errorf("exit_code = %q, want 0", result.Outputs["exit_code"])
	}
}

func TestDriver_Update_UnsupportedOp(t *testing.T) {
	d := &Driver{cli: &mockDockerClient{}}
	_, err := d.Update(context.Background(), "id", struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported op")
	}
}

// --- Delete ---

func TestDriver_Delete_HappyPath(t *testing.T) {
	var removedID string
	mock := &mockDockerClient{
		containerRemove: func(_ context.Context, id string, opts container.RemoveOptions) error {
			removedID = id
			if !opts.Force {
				t.Error("expected Force=true")
			}
			return nil
		},
	}
	d := &Driver{cli: mock}
	err := d.Delete(context.Background(), "container1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removedID != "container1" {
		t.Errorf("removed %q, want container1", removedID)
	}
}

func TestDriver_Delete_EmptyID(t *testing.T) {
	d := &Driver{cli: &mockDockerClient{}}
	err := d.Delete(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestDriver_Delete_NotFoundIsSuccess(t *testing.T) {
	mock := &mockDockerClient{
		containerRemove: func(_ context.Context, _ string, _ container.RemoveOptions) error {
			return notFoundError{msg: "not found"}
		},
	}
	d := &Driver{cli: mock}
	if err := d.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("Delete missing container: %v", err)
	}
}

// --- Allocate ---

func TestDriver_Allocate_ReturnsExecCommand(t *testing.T) {
	mock := &mockDockerClient{
		containerInspect: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return runningInspect("abc", "boxy-test"), nil
		},
	}
	d := &Driver{cli: mock}
	info, err := d.Allocate(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info["access"] != "docker-exec" {
		t.Errorf("access = %q, want docker-exec", info["access"])
	}
	exec, _ := info["exec"].(string)
	if !strings.Contains(exec, "boxy-test") {
		t.Errorf("exec %q should contain container name", exec)
	}
}

// --- parseMemoryBytes ---

func TestParseMemoryBytes(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"512m", 512 * 1024 * 1024},
		{"1g", 1024 * 1024 * 1024},
		{"2048", 2048},
		{"1024k", 1024 * 1024},
		{"", 0},
	}
	for _, tc := range cases {
		got, err := parseMemoryBytes(tc.input)
		if err != nil {
			t.Errorf("parseMemoryBytes(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseMemoryBytes(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// --- interface compliance ---

var _ providersdk.Driver = (*Driver)(nil)
