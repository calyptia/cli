package main

import (
	"github.com/spf13/cobra"
)

func newCmdCreateCoreInstance(config *config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "core_instance",
		Short: "Setup a new core instance on either Kubernetes, Amazon EC2 (TODO), or Google Compute Engine (TODO)",
	}
	cmd.AddCommand(newCmdCreateCoreInstanceOnK8s(config, nil))
	cmd.AddCommand(newCmdCreateCoreInstanceOnAWS(config, nil))
	cmd.AddCommand(newCmdCreateCoreInstanceOnGCP(config))
	return cmd
}
