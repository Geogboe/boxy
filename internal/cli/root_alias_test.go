package cli

import "testing"

// TestCommandAliasesResolve pins the short aliases added for #104: each
// alias must resolve (via cobra's own alias matching in Command.Find) to
// the same command as its full name, so adding a new alias later just means
// adding another entry to this table -- no new plumbing.
func TestCommandAliasesResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alias string
		full  string
	}{
		{alias: "sbx", full: "sandbox"},
		{alias: "cfg", full: "config"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()

			root := NewRootCommand()
			aliasCmd, _, err := root.Find([]string{tc.alias})
			if err != nil {
				t.Fatalf("Find(%q) error: %v", tc.alias, err)
			}

			root = NewRootCommand()
			fullCmd, _, err := root.Find([]string{tc.full})
			if err != nil {
				t.Fatalf("Find(%q) error: %v", tc.full, err)
			}

			if aliasCmd.Name() != fullCmd.Name() {
				t.Fatalf("alias %q resolved to %q, want %q", tc.alias, aliasCmd.Name(), fullCmd.Name())
			}
		})
	}
}
