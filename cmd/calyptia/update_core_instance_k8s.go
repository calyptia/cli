package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	cloud "github.com/calyptia/api/types"
	"github.com/calyptia/cli/k8s"
)

const calyptiaCoreImageIndexURL = "https://raw.githubusercontent.com/calyptia/core-images-index/main/container.index.json"

func newCmdUpdateCoreInstanceK8s(config *config, testClientSet kubernetes.Interface) *cobra.Command {
	var newVersion, newName string
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	cmd := &cobra.Command{
		Use:               "kubernetes CORE_INSTANCE",
		Aliases:           []string{"kube", "k8s"},
		Short:             "update a core instance from kubernetes",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: config.completeAggregators,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			aggregatorKey := args[0]

			if newVersion != "" {
				tags, err := getCoreImageTags()
				if err != nil {
					return err
				}
				err = VerifyCoreVersion(newVersion, tags)
				if err != nil {
					return err
				}
			}

			aggregatorID, err := config.loadAggregatorID(aggregatorKey)
			if aggregatorKey == newName {
				return fmt.Errorf("cannot update core instance with the same name")
			}
			if newName != "" {
				err = config.cloud.UpdateAggregator(config.ctx, aggregatorID, cloud.UpdateAggregator{
					Name: &newName,
				})
				if err != nil {
					return fmt.Errorf("could not update core instance: %w", err)
				}
			}
			if err != nil {
				return err
			}
			agg, err := config.cloud.Aggregator(ctx, aggregatorID)
			if err != nil {
				return err
			}

			if configOverrides.Context.Namespace == "" {
				configOverrides.Context.Namespace = apiv1.NamespaceDefault
			}

			var clientset kubernetes.Interface
			if testClientSet != nil {
				clientset = testClientSet
			} else {
				kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
				kubeClientConfig, err := kubeConfig.ClientConfig()
				if err != nil {
					return err
				}

				clientset, err = kubernetes.NewForConfig(kubeClientConfig)
				if err != nil {
					return err
				}

			}

			k8sClient := &k8s.Client{
				Interface:    clientset,
				Namespace:    configOverrides.Context.Namespace,
				ProjectToken: config.projectToken,
				CloudBaseURL: config.baseURL,
			}

			if err := k8sClient.EnsureOwnNamespace(ctx); err != nil {
				return fmt.Errorf("could not ensure kubernetes namespace exists: %w", err)
			}

			deploymentName := agg.Name + "-deployment"
			if err := k8sClient.UpdateDeployment(ctx, deploymentName, coreDockerImage, newVersion); err != nil {
				return fmt.Errorf("could not update deployment %s: %w", deploymentName, err)
			}

			if err != nil {
				return err
			}
			cmd.Printf("calyptia-core instance %q was successfully updated to version %s\n", agg.Name, newVersion)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&newVersion, "version", "", "New version of the calyptia-core instance")
	fs.StringVar(&newName, "name", "", "New core instance name")
	clientcmd.BindOverrideFlags(configOverrides, fs, clientcmd.RecommendedConfigOverrideFlags("kube-"))
	return cmd
}

func VerifyCoreVersion(newVersion string, coreVersions []string) error {
	if !contains(coreVersions, newVersion) {
		return fmt.Errorf("version %s is not available", newVersion)
	}
	return nil
}

func getCoreImageTags() ([]string, error) {
	var availableVersions []string
	get, err := http.Get(calyptiaCoreImageIndexURL)
	if err != nil {
		return nil, fmt.Errorf("could not get available core versions: %w", err)
	}
	defer get.Body.Close()
	err = json.NewDecoder(get.Body).Decode(&availableVersions)
	if err != nil {
		return nil, err
	}
	return availableVersions, nil
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
