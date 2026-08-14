//go:build windows

package svcmgr

import (
	"encoding/xml"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls   [][]string
	outputs map[string][]byte
	errs    map[string]error
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, " ")
	return f.outputs[key], f.errs[key]
}

func withFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	f := &fakeRunner{outputs: map[string][]byte{}, errs: map[string]error{}}
	orig := runCommand
	runCommand = f.run
	t.Cleanup(func() { runCommand = orig })
	return f
}

func TestRenderTaskXML_ContainsLogonTriggerHiddenAndRestart(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		DisplayName: "Boxy Agent",
		Description: "Boxy remote agent",
		ExecPath:    `C:\Users\testuser\.local\bin\boxy.exe`,
		Args:        []string{"agent", "serve", "--service-config", `C:\Users\testuser\.boxy-agent\service.yaml`},
	}
	xml := renderTaskXML(spec)
	for _, want := range []string{
		"<LogonTrigger>",
		"<Hidden>true</Hidden>",
		"<RestartOnFailure>",
		`<Command>C:\Users\testuser\.local\bin\boxy.exe</Command>`,
		`<Arguments>agent serve --service-config C:\Users\testuser\.boxy-agent\service.yaml</Arguments>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("rendered task XML missing %q; got:\n%s", want, xml)
		}
	}
}

// decodedExecXML holds the fields of a rendered task XML document that
// matter for these tests, decoded through encoding/xml so that entity
// references (&amp;, &#34;, ...) come back out as the real characters —
// exactly as Task Scheduler's own XML parser would hand them to
// CommandLineToArgvW, rather than as raw escaped text.
type decodedExecXML struct {
	Description string `xml:"RegistrationInfo>Description"`
	Command     string `xml:"Actions>Exec>Command"`
	Arguments   string `xml:"Actions>Exec>Arguments"`
}

// decodeRenderedTaskXML parses rendered (the string renderTaskXML
// produces) as XML and returns its decoded field values. rendered is
// plain Go UTF-8 text — the UTF-16LE+BOM re-encoding only happens later,
// in Install, via utf16LEWithBOM — so the declared "UTF-16" charset is
// swapped for "UTF-8" before parsing to match the actual bytes.
func decodeRenderedTaskXML(t *testing.T, rendered string) decodedExecXML {
	t.Helper()
	parseable := strings.Replace(rendered, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	var doc decodedExecXML
	if err := xml.Unmarshal([]byte(parseable), &doc); err != nil {
		t.Fatalf("rendered task XML does not parse: %v\n%s", err, rendered)
	}
	return doc
}

// parseWindowsArgsForTest splits a command-line string using the same
// rules CommandLineToArgvW (and therefore Task Scheduler) uses to parse
// <Arguments>. It exists only to prove, by round-tripping, that
// quoteWindowsArg's output is parsed back into the original argument
// values.
func parseWindowsArgsForTest(cmd string) []string {
	var args []string
	var cur strings.Builder
	inQuotes := false
	started := false
	n := len(cmd)
	i := 0
	for i < n {
		c := cmd[i]
		switch {
		case c == '\\':
			j := i
			for j < n && cmd[j] == '\\' {
				j++
			}
			numSlashes := j - i
			if j < n && cmd[j] == '"' {
				cur.WriteString(strings.Repeat(`\`, numSlashes/2))
				started = true
				if numSlashes%2 == 1 {
					cur.WriteByte('"')
					j++
				} else {
					inQuotes = !inQuotes
					j++
				}
			} else {
				cur.WriteString(strings.Repeat(`\`, numSlashes))
				started = true
			}
			i = j
		case c == '"':
			inQuotes = !inQuotes
			started = true
			i++
		case (c == ' ' || c == '\t') && !inQuotes:
			if started {
				args = append(args, cur.String())
				cur.Reset()
				started = false
			}
			i++
		default:
			cur.WriteByte(c)
			started = true
			i++
		}
	}
	if started {
		args = append(args, cur.String())
	}
	return args
}

func TestRenderTaskXML_ArgumentsWithSpacesRoundTrip(t *testing.T) {
	pathWithSpace := `C:\Users\Test User\.boxy-agent\service.yaml`
	spec := Spec{
		Name:     "boxy-agent",
		ExecPath: `C:\boxy.exe`,
		Args:     []string{"agent", "serve", "--service-config", pathWithSpace},
	}
	rendered := renderTaskXML(spec)
	doc := decodeRenderedTaskXML(t, rendered)
	argsValue := doc.Arguments

	// Structural check: the space-containing path must appear as one
	// contiguous quoted token, not bare (which is what the pre-fix
	// strings.Join(spec.Args, " ") produced and Task Scheduler would then
	// split into extra stray tokens on the space).
	wantQuoted := `"` + pathWithSpace + `"`
	if !strings.Contains(argsValue, wantQuoted) {
		t.Fatalf("expected quoted path %q inside <Arguments> value %q", wantQuoted, argsValue)
	}

	// Round-trip check: parsing the rendered command line the way
	// CommandLineToArgvW would must reproduce the original, unsplit
	// argument list.
	got := parseWindowsArgsForTest(argsValue)
	want := []string{"agent", "serve", "--service-config", pathWithSpace}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-tripped args = %#v, want %#v (raw <Arguments> value: %q)", got, want, argsValue)
	}
}

func TestRenderTaskXML_EscapesXMLSpecialCharacters(t *testing.T) {
	spec := Spec{
		Name:        "boxy-agent",
		Description: "Boxy & Friends <Agent>",
		ExecPath:    `C:\Program Files\Boxy & Co\boxy.exe`,
		Args:        []string{"--tag", `A&B`},
	}
	rendered := renderTaskXML(spec)

	for _, want := range []string{
		"Boxy &amp; Friends &lt;Agent&gt;",
		`C:\Program Files\Boxy &amp; Co\boxy.exe`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected escaped text %q in rendered XML:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "A&amp;B") {
		t.Errorf("expected escaped arg %q in rendered XML:\n%s", "A&amp;B", rendered)
	}

	// Prove the result is actually well-formed XML — parsing it back and
	// confirming entities decode to the original characters — rather than
	// malformed XML that schtasks /create /xml would fail to parse. The
	// string renderTaskXML returns is plain Go UTF-8 text (the UTF-16LE+BOM
	// re-encoding happens later, in Install, via utf16LEWithBOM), so the
	// declared "UTF-16" charset is swapped for "UTF-8" to match the actual
	// bytes before decoding.
	doc := decodeRenderedTaskXML(t, rendered)
	if doc.Description != spec.Description {
		t.Errorf("decoded Description = %q, want %q", doc.Description, spec.Description)
	}
	if doc.Command != spec.ExecPath {
		t.Errorf("decoded Command = %q, want %q", doc.Command, spec.ExecPath)
	}
	if doc.Arguments != "--tag A&B" {
		t.Errorf("decoded Arguments = %q, want %q", doc.Arguments, "--tag A&B")
	}
}

func TestQuoteWindowsArg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `""`},
		{"no special chars", "agent", "agent"},
		{"plain path no spaces", `C:\boxy\service.yaml`, `C:\boxy\service.yaml`},
		{"space", `John Doe`, `"John Doe"`},
		{"embedded quote", `say "hi"`, `"say \"hi\""`},
		{"trailing backslash before close", `C:\dir with space\`, `"C:\dir with space\\"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := quoteWindowsArg(tc.in)
			if got != tc.want {
				t.Fatalf("quoteWindowsArg(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Every non-empty case must also round-trip through the
			// CommandLineToArgvW-style parser back to the original value.
			parsed := parseWindowsArgsForTest(got)
			if len(parsed) != 1 || parsed[0] != tc.in {
				t.Fatalf("round-trip of quoteWindowsArg(%q) = %#v, want single arg %q", tc.in, parsed, tc.in)
			}
		})
	}
}

func TestTaskSchedulerManager_Install_RunsSchtasksCreate(t *testing.T) {
	f := withFakeRunner(t)
	m := &taskSchedulerManager{}
	spec := Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`, Args: []string{"agent", "serve"}}

	if err := m.Install(spec); err != nil {
		t.Fatalf("Install: %v", err)
	}
	var create []string
	for _, c := range f.calls {
		if len(c) > 1 && c[0] == "schtasks" && c[1] == "/create" {
			create = c
		}
	}
	if create == nil {
		t.Fatalf("expected a schtasks /create call, got: %v", f.calls)
	}
	joined := strings.Join(create, " ")
	for _, want := range []string{"/create", "/tn", "boxy-agent", "/xml", "/f"} {
		if !strings.Contains(joined, want) {
			t.Errorf("schtasks call missing %q: %s", want, joined)
		}
	}
}

func TestTaskSchedulerManager_Install_AlreadyInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	m := &taskSchedulerManager{}
	f.outputs["schtasks /query /tn boxy-agent"] = []byte("boxy-agent  Ready")

	err := m.Install(Spec{Name: "boxy-agent", ExecPath: `C:\boxy.exe`})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("Install error = %v, want ErrAlreadyInstalled", err)
	}
}

func TestTaskSchedulerManager_Uninstall_NotInstalled_Errors(t *testing.T) {
	f := withFakeRunner(t)
	f.errs["schtasks /query /tn boxy-agent"] = errors.New("ERROR: The system cannot find the file specified.")
	m := &taskSchedulerManager{}

	if err := m.Uninstall("boxy-agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Uninstall error = %v, want ErrNotInstalled", err)
	}
}

func TestTaskSchedulerManager_Status_ReportsUserTaskMode(t *testing.T) {
	f := withFakeRunner(t)
	f.outputs["schtasks /query /tn boxy-agent"] = []byte("TaskName  Status\r\nboxy-agent  Running\r\n")
	m := &taskSchedulerManager{}

	st, err := m.Status("boxy-agent")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running || st.Mode != "user-task" {
		t.Fatalf("Status = %+v, want Installed=true Running=true Mode=user-task", st)
	}
}
