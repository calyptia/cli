package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	cloud "github.com/calyptia/api/types"
)

var (
	deploymentReplicas           int32 = 1
	automountServiceAccountToken       = true
)

type Client struct {
	kubernetes.Interface
	Namespace    string
	ProjectToken string
	CloudBaseURL string
	LabelsFunc   func() map[string]string
}

func (client *Client) EnsureOwnNamespace(ctx context.Context) error {
	exists, err := client.ownNamespaceExists(ctx)
	if err != nil {
		return fmt.Errorf("exists: %w", err)
	}

	if exists {
		return nil
	}

	_, err = client.createOwnNamespace(ctx)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}

	return nil
}

func (client *Client) ownNamespaceExists(ctx context.Context) (bool, error) {
	_, err := client.CoreV1().Namespaces().Get(ctx, client.Namespace, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

func (client *Client) createOwnNamespace(ctx context.Context) (*apiv1.Namespace, error) {
	return client.CoreV1().Namespaces().Create(ctx, &apiv1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: client.Namespace,
		},
	}, metav1.CreateOptions{})
}

func (client *Client) CreateSecret(ctx context.Context, name string, value []byte) (*apiv1.Secret, error) {
	return client.CoreV1().Secrets(client.Namespace).Create(ctx, &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: client.LabelsFunc(),
		},
		Data: map[string][]byte{
			name: value,
		},
	}, metav1.CreateOptions{})
}

func (client *Client) CreateClusterRole(ctx context.Context, agg cloud.CreatedAggregator) (*rbacv1.ClusterRole, error) {
	return client.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   agg.Name + "-cluster-role",
			Labels: client.LabelsFunc(),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"", "apps"},
				Resources: []string{
					"namespaces",
					"deployments",
					"replicasets",
					"pods",
					"services",
					"configmaps",
					"deployments/scale",
					"secrets",
				},
				Verbs: []string{
					"get",
					"list",
					"create",
					"delete",
					"patch",
					"update",
					"watch",
					"deletecollection",
				},
			},
		},
	}, metav1.CreateOptions{})
}

func (client *Client) CreateServiceAccount(ctx context.Context, agg cloud.CreatedAggregator) (*apiv1.ServiceAccount, error) {
	return client.CoreV1().ServiceAccounts(client.Namespace).Create(ctx, &apiv1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:   agg.Name + "-service-account",
			Labels: client.LabelsFunc(),
		},
	}, metav1.CreateOptions{})
}

func (client *Client) CreateClusterRoleBinding(
	ctx context.Context,
	agg cloud.CreatedAggregator,
	clusterRole *rbacv1.ClusterRole,
	serviceAccount *apiv1.ServiceAccount,
) (*rbacv1.ClusterRoleBinding, error) {
	return client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   agg.Name + "-cluster-role-binding",
			Labels: client.LabelsFunc(),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Namespace: client.Namespace,
				Name:      serviceAccount.Name,
			},
		},
	}, metav1.CreateOptions{})
}

func (client *Client) CreateDeployment(
	ctx context.Context,
	image string,
	agg cloud.CreatedAggregator,
	serviceAccount *apiv1.ServiceAccount,
) (*appsv1.Deployment, error) {
	labels := client.LabelsFunc()
	return client.AppsV1().Deployments(client.Namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:   agg.Name + "-deployment",
			Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &deploymentReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					ServiceAccountName:           serviceAccount.Name,
					AutomountServiceAccountToken: &automountServiceAccountToken,
					Containers: []apiv1.Container{
						{
							Name:            agg.Name,
							Image:           image,
							ImagePullPolicy: apiv1.PullAlways,
							Args:            []string{"-debug=true"},
							Env: []apiv1.EnvVar{
								{
									Name:  "AGGREGATOR_NAME",
									Value: agg.Name,
								},
								{
									Name:  "PROJECT_TOKEN",
									Value: client.ProjectToken,
								},
								{
									Name:  "AGGREGATOR_FLUENTBIT_CLOUD_URL",
									Value: client.CloudBaseURL,
								},
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
}

func (client *Client) DeleteDeploymentByLabel(ctx context.Context, label, ns string) error {
	foreground := metav1.DeletePropagationForeground
	return client.AppsV1().Deployments(ns).DeleteCollection(ctx, metav1.DeleteOptions{
		PropagationPolicy: &foreground,
	}, metav1.ListOptions{LabelSelector: label})
}

func (client *Client) DeleteClusterRoleByLabel(ctx context.Context, label string) error {
	return client.RbacV1().ClusterRoles().DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: label})
}

func (client *Client) DeleteServiceAccountByLabel(ctx context.Context, label, ns string) error {
	return client.CoreV1().ServiceAccounts(ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: label})
}

func (client *Client) DeleteRoleBindingByLabel(ctx context.Context, label string) error {
	return client.RbacV1().ClusterRoleBindings().DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: label})
}

func (client *Client) DeleteServiceByName(ctx context.Context, name, ns string) error {
	return client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func (client *Client) DeleteSecretByLabel(ctx context.Context, label, ns string) error {
	return client.CoreV1().Secrets(ns).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: label})
}

func (client *Client) FindServicesByLabel(ctx context.Context, label, ns string) (*apiv1.ServiceList, error) {
	return client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: label})
}

func (client *Client) UpdateDeployment(ctx context.Context, name, image, version string) error {
	deployment, err := client.FindDeploymentByName(ctx, name)
	if err != nil {
		return err
	}
	deployment.Spec.Template.Spec.Containers[0].Image = fmt.Sprintf("%s:%s", image, version)
	_, err = client.AppsV1().Deployments(client.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	return err
}

func (client *Client) FindDeploymentByName(ctx context.Context, name string) (*appsv1.Deployment, error) {
	deployment, err := client.AppsV1().Deployments(client.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return deployment, nil
}
