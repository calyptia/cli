package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newCmdDeleteEndpoint(config *config) *cobra.Command {
	var confirmed bool
	cmd := &cobra.Command{
		Use:               "endpoint ENDPOINT",
		Short:             "Delete a single endpoint by ID",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: config.completePipelines,
		RunE: func(cmd *cobra.Command, args []string) error {
			portID := args[0]
			if !confirmed {
				fmt.Printf("Are you sure you want to delete %q? (y/N) ", portID)
				var answer string
				_, err := fmt.Scanln(&answer)
				if err != nil && err.Error() == "unexpected newline" {
					err = nil
				}

				if err != nil {
					return fmt.Errorf("could not to read answer: %v", err)
				}

				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					return nil
				}
			}

			err := config.cloud.DeletePipelinePort(config.ctx, portID)
			if err != nil {
				return fmt.Errorf("could not delete endpoint: %w", err)
			}

			return nil
		},
	}

	fs := cmd.Flags()
	fs.BoolVarP(&confirmed, "yes", "y", false, "Confirm deletion")

	return cmd
}
