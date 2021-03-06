package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/turnerlabs/fargate/console"
	EC2 "github.com/turnerlabs/fargate/ec2"
	ECS "github.com/turnerlabs/fargate/ecs"
	"github.com/spf13/cobra"
)

type ServiceProcessListOperation struct {
	ServiceName string
}

var servicePsCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running tasks for a service",
	Run: func(cmd *cobra.Command, args []string) {
		operation := &ServiceProcessListOperation{
			ServiceName: getServiceName(),
		}

		getServiceProcessList(operation)
	},
}

func init() {
	serviceCmd.AddCommand(servicePsCmd)
}

func getServiceProcessList(operation *ServiceProcessListOperation) {
	var eniIds []string

	ecs := ECS.New(sess, getClusterName())
	ec2 := EC2.New(sess)
	tasks := ecs.DescribeTasksForService(operation.ServiceName)

	for _, task := range tasks {
		if task.EniId != "" {
			eniIds = append(eniIds, task.EniId)
		}
	}

	if len(tasks) > 0 {
		enis := ec2.DescribeNetworkInterfaces(eniIds)

		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 0, 8, 1, '\t', 0)
		fmt.Fprintln(w, "ID\tIMAGE\tSTATUS\tRUNNING\tIP\tCPU\tMEMORY\t")

		for _, t := range tasks {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				t.TaskId,
				t.Image,
				Humanize(t.LastStatus),
				t.RunningFor(),
				enis[t.EniId].PublicIpAddress,
				t.Cpu,
				t.Memory,
			)
		}

		w.Flush()
	} else {
		console.Info("No tasks found")
	}
}
