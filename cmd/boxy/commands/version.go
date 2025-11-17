package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Display the version, build date, and git commit information for boxy.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Boxy v%s\n", version)
		fmt.Printf("Git commit: %s\n", gitCommit)
		fmt.Printf("Built: %s\n", buildDate)
		fmt.Printf("Go version: %s\n", runtime.Version())
		fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
