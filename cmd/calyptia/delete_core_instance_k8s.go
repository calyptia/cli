package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/calyptia/cli/k8s"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func newCmdDeleteCoreInstanceK8s(config *config, testClientSet kubernetes.Interface) *cobra.Command {
	isNonInteractiveMode := os.Stdin == nil || !term.IsTerminal(int(os.Stdin.Fd()))

	var skipError, confirmDelete bool
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	cmd := &cobra.Command{
		Use:               "kubernetes CORE_INSTANCE",
		Aliases:           []string{"kube", "k8s"},
		Short:             "Delete a core instance from kubernetes",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: config.completeAggregators,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			aggregatorKey := args[0]

			aggregatorID, err := config.loadAggregatorID(aggregatorKey)
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

			label := fmt.Sprintf("%s=%s", k8s.LabelAggregatorID, agg.ID)
			err = listDeletionsByLabel(ctx, k8sClient, cmd, label)
			if err != nil {
				return err
			}

			cmd.Println("You confirm the deletion of those resources? [Y/n]")

			if !confirmDelete && !isNonInteractiveMode {
				confirmDelete = ask(cmd.InOrStdin(), cmd.ErrOrStderr())
			}

			if !confirmDelete {
				cmd.Println("operation canceled")
				return nil
			}

			namespaces, err := k8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}
			for _, ns := range namespaces.Items {

				err = k8sClient.DeleteDeploymentByLabel(ctx, label, ns.Name)
				if err != nil {
					if !skipError {
						return err
					}
					cmd.PrintErrf("a problem occurred while deleting deployments, err: %v\n", err)
				}

				services, err := k8sClient.FindServicesByLabel(ctx, label, ns.Name)
				if err != nil {
					return err
				}

				for _, item := range services.Items {
					err := k8sClient.DeleteServiceByName(ctx, item.Name, ns.Name)
					if err != nil {
						if !skipError {
							return err
						}
						cmd.PrintErrf("a problem occurred while deleting %q, err: %v\n", item.Name, err)
					}
				}

				serviceAcc := agg.Name + "-service-account"
				err = k8sClient.DeleteServiceAccountByLabel(ctx, label, ns.Name)
				if err != nil {
					if !skipError {
						return err
					}
					cmd.PrintErrf("a problem occurred while deleting %q, err: %v\n", serviceAcc, err)
				}

				secret := agg.Name + "-private-rsa.key"
				err = k8sClient.DeleteSecretByLabel(ctx, label, ns.Name)
				if err != nil {
					if !skipError {
						return err
					}
					cmd.PrintErrf("a problem occurred while deleting %q, err: %v\n", secret, err)
				}
			}
			clusterRole := agg.Name + "-cluster-role"
			err = k8sClient.DeleteClusterRoleByLabel(ctx, label)
			if err != nil {
				if !skipError {
					return err
				}
				cmd.PrintErrf("a problem occurred while deleting %q, err: %v\n", clusterRole, err)
			}
			roleBinding := agg.Name + "-cluster-role-binding"
			err = k8sClient.DeleteRoleBindingByLabel(ctx, label)
			if err != nil {
				if !skipError {
					return err
				}
				cmd.PrintErrf("a problem occurred while deleting %q, err: %v\n", roleBinding, err)
			}

			err = config.cloud.DeleteAggregator(ctx, agg.ID)
			if err != nil {
				return err
			}
			cmd.Printf("calyptia-core instance %q was successfully deleted\n", agg.Name)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.BoolVar(&skipError, "skip-error", false, "Skip errors during delete process")

	fs.BoolVar(&confirmDelete, "yes", isNonInteractiveMode, "Confirm deletion")
	clientcmd.BindOverrideFlags(configOverrides, fs, clientcmd.RecommendedConfigOverrideFlags("kube-"))
	return cmd
}

func listDeletionsByLabel(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string) error {
	namespaces, err := k8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, ns := range namespaces.Items {
		err := listDeployments(ctx, k8sClient, cmd, label, ns.Name)
		if err != nil {
			return err
		}
		err = listServices(ctx, k8sClient, cmd, label, ns.Name)
		if err != nil {
			return err
		}
		err = listServiceAccounts(ctx, k8sClient, cmd, label, ns.Name)
		if err != nil {
			return err
		}

		err = listSecrets(ctx, k8sClient, cmd, label, ns.Name)
		if err != nil {
			return err
		}

	}
	err = listRoleBindings(ctx, k8sClient, cmd, label)
	if err != nil {
		return err
	}
	err = listClusterRoles(ctx, k8sClient, cmd, label)
	if err != nil {
		return err
	}
	return nil
}

func listSecrets(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string, ns string) error {
	secrets, err := k8sClient.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return err
	}
	if len(secrets.Items) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secrets:\n")
	for _, item := range secrets.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "%s - %s\n", ns, item.Name)
	}
	return nil
}

func listRoleBindings(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string) error {
	roleBindings, err := k8sClient.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return err
	}
	if len(roleBindings.Items) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Role bindings:\n")
	for _, item := range roleBindings.Items {
		cmd.Printf("%s\n", item.Name)
	}
	return nil
}

func listServiceAccounts(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string, ns string) error {
	serviceAccounts, err := k8sClient.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return err
	}
	if len(serviceAccounts.Items) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Service accounts:\n")
	for _, item := range serviceAccounts.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "%s - %s\n", ns, item.Name)
	}
	return nil
}

func listClusterRoles(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string) error {
	clusterRoles, err := k8sClient.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return err
	}
	if len(clusterRoles.Items) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Cluster roles:\n")
	for _, item := range clusterRoles.Items {
		cmd.Printf("%s\n", item.Name)
	}
	return nil
}

func listServices(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string, ns string) error {
	services, err := k8sClient.CoreV1().Services(ns).List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return err
	}
	if len(services.Items) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Services:\n")
	for _, item := range services.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "%s - %s\n", ns, item.Name)
	}
	return nil
}

func listDeployments(ctx context.Context, k8sClient *k8s.Client, cmd *cobra.Command, label string, ns string) error {
	deployments, err := k8sClient.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return err
	}
	if len(deployments.Items) == 0 {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deployments:\n")
	for _, item := range deployments.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "%s - %s\n", ns, item.Name)
	}
	return nil
}

func ask(rd io.Reader, w io.Writer) bool {
	reader := bufio.NewReader(rd)
	for {
		s, _ := reader.ReadString('\n')
		s = strings.TrimSuffix(s, "\n")
		s = strings.ToLower(s)
		if len(s) > 1 {
			fmt.Fprintln(w, "Please enter Y or N")
			continue
		}
		if strings.Compare(s, "n") == 0 {
			return false
		} else if strings.Compare(s, "y") == 0 {
			break
		} else {
			fmt.Fprintln(w, "Please enter Y or N")
			continue
		}
	}
	return true
}
