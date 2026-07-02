package cmd

import (
	"fmt"
	"strings"

	"github.com/aliyun/infraguard/pkg/i18n"
	"github.com/open-policy-agent/opa/version"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags
var Version = "0.10.1"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information", // Will be updated by i18n
	Long:  "",                          // Will be updated by i18n
	Run: func(cmd *cobra.Command, args []string) {
		msg := i18n.Msg()
		// Remove 'v' prefix from version for consistent display
		versionDisplay := strings.TrimPrefix(Version, "v")
		fmt.Printf("%s: %s\n", msg.Version.InfraGuard, versionDisplay)
		fmt.Printf("%s: %s\n", msg.Version.OPA, version.Version)
	},
}

// updateVersionCommandDescriptions updates the version command descriptions with i18n
func updateVersionCommandDescriptions() {
	versionCmd.Short = i18n.Get(func(m *i18n.Messages) string { return m.Version.Short })
	versionCmd.Long = i18n.Get(func(m *i18n.Messages) string { return m.Version.Long })
}
