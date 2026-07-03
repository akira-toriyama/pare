package cli

import (
	"encoding/json"
	"fmt"

	"github.com/akira-toriyama/pare/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Long: "Print pare's build identity. With --json, emit it as a single JSON object\n" +
			"(version, commit, date, go) for scripts and agents.",
		Example: "  pare version\n  pare version --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			if asJSON {
				b, err := json.Marshal(info)
				if err != nil {
					return internalErr("marshalling version: %v", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pare %s\n", info.Human())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output build info as JSON")
	return cmd
}
