package cmd

import (
	"github.com/spf13/cobra"
	"github.com/turnerlabs/fargate/console"
	"github.com/turnerlabs/fargate/dockercompose"
	ECS "github.com/turnerlabs/fargate/ecs"
	"github.com/turnerlabs/fargate/sts"
)

// ServiceDeployOperation represents a deploy operation
type ServiceDeployOperation struct {
	ServiceName string
	Image       string
	ComposeFile string
	Region      string
	Revision    string
}

const deployDockerComposeLabel = "aws.ecs.fargate.deploy"

var flagServiceDeployImage string
var flagServiceDeployDockerComposeFile string
var flagServiceDeployDockerComposeImageOnly bool
var flagServiceDeployRevision string

var serviceDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy applications to services",
	Long: `Deploy applications to services

The Docker container image to use in the service can be specified
via the --image flag.

The docker-compose.yml format is also supported using the --file flag.
If -f is specified, the image and the environment variables in the
docker-compose.yml file will be deployed.

A task definition revision can be specified via the --revision flag.
The revision number can either be absolute or a delta specified with a sign
such as +5 or -2, where -2 is "2 configurations ago" from the current
deployed revision.
`,
	Example: `
fargate service deploy -i 123456789.dkr.ecr.us-east-1.amazonaws.com/my-service:1.0
fargate service deploy -f docker-compose.yml
fargate service deploy -r 37
`,
	Run: func(cmd *cobra.Command, args []string) {
		operation := &ServiceDeployOperation{
			ServiceName: getServiceName(),
			Region:      region,
			Image:       flagServiceDeployImage,
			ComposeFile: flagServiceDeployDockerComposeFile,
			Revision:    flagServiceDeployRevision,
		}

		if !validateFlags(operation) {
			cmd.Help()
			return
		}

		deployService(operation)
	},
}

func init() {
	serviceDeployCmd.Flags().StringVarP(&flagServiceDeployImage, "image", "i", "", "Docker image to run in the service")

	serviceDeployCmd.Flags().StringVarP(&flagServiceDeployRevision, "revision", "r", "", "Task definition revision number")

	serviceDeployCmd.Flags().StringVarP(&flagServiceDeployDockerComposeFile, "file", "f", "", "Specify a docker-compose.yml file to deploy. The image and environment variables in the file will be deployed.")

	serviceDeployCmd.Flags().BoolVar(&flagServiceDeployDockerComposeImageOnly, "image-only", false, "Only deploy the image when a docker-compose.yml file is specified.")

	serviceCmd.AddCommand(serviceDeployCmd)
}

func deployService(operation *ServiceDeployOperation) {

	if operation.ComposeFile != "" {
		deployDockerComposeFile(operation)
		return
	}

	if operation.Revision != "" {
		deployRevision(operation)
		return
	}

	ecs := ECS.New(sess, getClusterName())
	service := ecs.DescribeService(operation.ServiceName)

	taskDefinitionArn := ecs.UpdateTaskDefinitionImage(service.TaskDefinitionArn, operation.Image)
	ecs.UpdateServiceTaskDefinition(operation.ServiceName, taskDefinitionArn)
	console.Info("Deployed %s to service %s", operation.Image, operation.ServiceName)
}

//deploy a docker-compose.yml file to fargate
func deployDockerComposeFile(operation *ServiceDeployOperation) {
	var taskDefinitionArn string

	ecs := ECS.New(sess, getClusterName())
	ecsService := ecs.DescribeService(operation.ServiceName)

	dockerService := getDockerServiceFromComposeFile(operation.ComposeFile)

	envvars := convertDockerComposeEnvVarsToECSEnvVars(dockerService)
	secrets := convertDockerComposeSecretsToECSSecrets(dockerService)

	//if --image-only flag is set, update image only
	if flagServiceDeployDockerComposeImageOnly {
		//register a new task definition based on the image from the compose file
		taskDefinitionArn = ecs.UpdateTaskDefinitionImage(ecsService.TaskDefinitionArn, dockerService.Image)
	} else {
		//register a new task definition based on the image and environment variables from the compose file
		taskDefinitionArn = ecs.UpdateTaskDefinitionImageAndEnvVars(ecsService.TaskDefinitionArn, dockerService.Image, envvars, true, secrets)
	}

	//update service with new task definition
	ecs.UpdateServiceTaskDefinition(operation.ServiceName, taskDefinitionArn)

	console.Info("Deployed %s to service %s as revision %s", operation.ComposeFile, operation.ServiceName, ecs.GetRevisionNumber(taskDefinitionArn))
}

func deployRevision(operation *ServiceDeployOperation) {
	ecs := ECS.New(sess, getClusterName())
	service := ecs.DescribeService(operation.ServiceName)

	sts := sts.New(sess)
	account := sts.GetCallerIdentity().Account

	//build full task definiton arn with revision
	revisionNumber := ecs.ResolveRevisionNumber(service.TaskDefinitionArn, operation.Revision)
	taskFamily := ecs.GetTaskFamily(service.TaskDefinitionArn)

	if revisionNumber == "" {
		console.IssueExit("Could not resolve revision number")
	}

	taskDefinitionArn := ecs.GetTaskDefinitionARN(operation.Region, account, taskFamily, revisionNumber)

	ecs.UpdateServiceTaskDefinition(operation.ServiceName, taskDefinitionArn)

	console.Info("Deployed revision %s to service %s.", revisionNumber, operation.ServiceName)
}

func getDockerServiceFromComposeFile(dockerComposeFile string) *dockercompose.Service {
	//read the compose file configuration
	composeFile := dockercompose.NewComposeFile(dockerComposeFile)
	dockerCompose := composeFile.Config()

	//determine which docker-compose service/container to deploy
	_, dockerService := getDockerServiceToDeploy(dockerCompose)
	if dockerService == nil {
		console.IssueExit(`Please indicate which docker container you'd like to deploy using the label "%s: 1"`, deployDockerComposeLabel)
	}

	return dockerService
}

func convertDockerComposeEnvVarsToECSEnvVars(service *dockercompose.Service) []ECS.EnvVar {
	result := []ECS.EnvVar{}
	for k, v := range service.Environment {
		result = append(result, ECS.EnvVar{
			Key:   k,
			Value: v,
		})
	}
	return result
}

func convertDockerComposeSecretsToECSSecrets(service *dockercompose.Service) []ECS.Secret {
	result := []ECS.Secret{}
	for k, v := range service.Secrets {
		result = append(result, ECS.Secret{
			Key:       k,
			ValueFrom: v,
		})
	}
	return result
}

//determine which docker-compose service/container to deploy
func getDockerServiceToDeploy(dc *dockercompose.DockerCompose) (string, *dockercompose.Service) {
	//look for label if there's more than 1
	var service *dockercompose.Service
	name := ""
	for k, v := range dc.Services {
		if len(dc.Services) == 1 {
			service = v
			name = k
			break
		}
		if v.Labels[deployDockerComposeLabel] == "1" {
			service = v
			name = k
			break
		}
	}
	return name, service
}

//Check incompatible flag combinations
func validateFlags(operation *ServiceDeployOperation) bool {
	strFlags := []string{operation.Image, operation.ComposeFile, operation.Revision}
	setFlags := make([]string, 0)

	for _, v := range strFlags {
		if v != "" {
			setFlags = append(setFlags, v)
		}
	}

	valid := len(setFlags) == 1

	return valid
}
