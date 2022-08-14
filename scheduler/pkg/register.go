package pkg

import (
	"github.com/spf13/cobra"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"
)

// Register registers the plugin to command
func Register() *cobra.Command {
	return app.NewSchedulerCommand(
		app.WithPlugin(PluginName, New),
	)
}
