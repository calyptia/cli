package main

import (
	"context"
	"fmt"
	cloud "github.com/calyptia/api/types"
	"github.com/calyptia/cli/k8s"
	"github.com/spf13/cobra"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	//nolint: gosec // this is not a secret leak, it's just a format declaration.
	secretNameFormat = "%s-private-rsa.key"
	coreDockerImage  = "ghcr.io/calyptia/core"
)

func newCmdCreateCoreInstanceOnK8s(config *config, testClientSet kubernetes.Interface) *cobra.Command {
	var coreInstanceName string
	var coreInstanceVersion string
	var noHealthCheckPipeline bool
	var environmentKey string
	var tags []string

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	cmd := &cobra.Command{
		Use:     "kubernetes",
		Aliases: []string{"kube", "k8s"},
		Short:   "Setup a new core instance on Kubernetes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			var coreDockerImage = coreDockerImage
			if coreInstanceVersion != "" {
				tags, err := getCoreImageTags()
				if err != nil {
					return err
				}
				err = VerifyCoreVersion(coreInstanceVersion, tags)
				if err != nil {
					return err
				}
				coreDockerImage = fmt.Sprintf("%s:%s", coreDockerImage, coreInstanceVersion)
			}

			var environmentID string
			if environmentKey != "" {
				var err error
				environmentID, err = config.loadEnvironmentID(environmentKey)
				if err != nil {
					return err
				}
			}

			created, err := config.cloud.CreateAggregator(ctx, cloud.CreateAggregator{
				Name:                   coreInstanceName,
				AddHealthCheckPipeline: !noHealthCheckPipeline,
				EnvironmentID:          environmentID,
				Tags:                   tags,
			})
			if err != nil {
				return fmt.Errorf("could not create core instance at calyptia cloud: %w", err)
			}

			if configOverrides.Context.Namespace == "" {
				configOverrides.Context.Namespace = apiv1.NamespaceDefault
			}

			var clientSet kubernetes.Interface
			if testClientSet != nil {
				clientSet = testClientSet
			} else {
				kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
				kubeClientConfig, err := kubeConfig.ClientConfig()
				if err != nil {
					return err
				}

				clientSet, err = kubernetes.NewForConfig(kubeClientConfig)
				if err != nil {
					return err
				}

			}

			k8sClient := &k8s.Client{
				Interface:    clientSet,
				Namespace:    configOverrides.Context.Namespace,
				ProjectToken: config.projectToken,
				CloudBaseURL: config.baseURL,
				LabelsFunc: func() map[string]string {
					return map[string]string{
						k8s.LabelVersion:      version,
						k8s.LabelPartOf:       "calyptia",
						k8s.LabelManagedBy:    "calyptia-cli",
						k8s.LabelCreatedBy:    "calyptia-cli",
						k8s.LabelProjectID:    config.projectID,
						k8s.LabelAggregatorID: created.ID,
					}
				},
			}

			if err := k8sClient.EnsureOwnNamespace(ctx); err != nil {
				return fmt.Errorf("could not ensure kubernetes namespace exists: %w", err)
			}

			secret, err := k8sClient.CreateSecret(ctx, fmt.Sprintf(secretNameFormat, created.Name), created.PrivateRSAKey)
			if err != nil {
				return fmt.Errorf("could not create kubernetes secret from private key: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "secret=%q\n", secret.Name)

			clusterRole, err := k8sClient.CreateClusterRole(ctx, created)
			if err != nil {
				return fmt.Errorf("could not create kubernetes cluster role: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "cluster_role=%q\n", clusterRole.Name)

			serviceAccount, err := k8sClient.CreateServiceAccount(ctx, created)
			if err != nil {
				return fmt.Errorf("could not create kubernetes service account: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "service_account=%q\n", serviceAccount.Name)

			binding, err := k8sClient.CreateClusterRoleBinding(ctx, created, clusterRole, serviceAccount)
			if err != nil {
				return fmt.Errorf("could not create kubernetes cluster role binding: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "cluster_role_binding=%q\n", binding.Name)

			deploy, err := k8sClient.CreateDeployment(ctx, coreDockerImage, created, serviceAccount)
			if err != nil {
				return fmt.Errorf("could not create kubernetes deployment: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "deployment=%q\n", deploy.Name)

			return nil
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&coreInstanceVersion, "version", "", "Core instance version (latest is the default)")
	fs.StringVar(&coreInstanceName, "name", "", "Core instance name (autogenerated if empty)")
	fs.BoolVar(&noHealthCheckPipeline, "no-health-check-pipeline", false, "Disable health check pipeline creation alongside the core instance")
	fs.StringVar(&environmentKey, "environment", "", "Calyptia environment name or ID")
	fs.StringSliceVar(&tags, "tags", nil, "Tags to apply to the core instance")
	clientcmd.BindOverrideFlags(configOverrides, fs, clientcmd.RecommendedConfigOverrideFlags("kube-"))

	_ = cmd.RegisterFlagCompletionFunc("environment", config.completeEnvironments)

	return cmd
}
