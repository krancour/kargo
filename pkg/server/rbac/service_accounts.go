package rbac

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rbacapi "github.com/akuity/kargo/api/rbac/v1alpha1"
)

// ServiceAccountsDatabase is an interface for the Kargo ServiceAccounts store.
// Note: Kargo ServiceAccounts are specially labeled Kubernetes ServiceAccounts
// with associated bearer tokens.
type ServiceAccountsDatabase interface {
	// Create creates a Kargo ServiceAccount.
	Create(context.Context, *corev1.ServiceAccount) (*corev1.ServiceAccount, error)
	// Delete deletes a Kargo ServiceAccount.
	Delete(ctx context.Context, project, name string) error
	// Get returns a Kargo ServiceAccount.
	Get(ctx context.Context, project, name string) (*corev1.ServiceAccount, error)
	// GetNewToken returns a new bearer token for a Kargo ServiceAccount. It
	// revokes any existing token.
	GetNewToken(ctx context.Context, project, name string) (string, error)
	// List returns a list of Kargo ServiceAccounts.
	List(ctx context.Context, project string) ([]corev1.ServiceAccount, error)
}

// serviceAccountsDatabase is an implementation of the ServiceAccountsDatabase
// interface that utilizes a Kubernetes controller runtime client to store and
// retrieve Kargo ServiceAccounts.
type serviceAccountsDatabase struct {
	client client.Client
}

// NewKubernetesServiceAccountsDatabase returns an implementation of the
// ServiceAccountsDatabase interface that utilizes a Kubernetes controller
// runtime client to store and retrieve Kargo ServiceAccounts.
func NewKubernetesServiceAccountsDatabase(
	c client.Client,
) ServiceAccountsDatabase {
	return &serviceAccountsDatabase{client: c}
}

// Create implements ServiceAccountsDatabase.
func (s *serviceAccountsDatabase) Create(
	ctx context.Context,
	sa *corev1.ServiceAccount,
) (*corev1.ServiceAccount, error) {
	if sa.Labels == nil {
		sa.Labels = make(map[string]string, 1)
	}
	sa.Labels[rbacapi.LabelKeyServiceAccount] = rbacapi.LabelValueTrue
	if sa.Annotations == nil {
		sa.Annotations = make(map[string]string, 1)
	}
	sa.Annotations[rbacapi.AnnotationKeyManaged] = rbacapi.AnnotationValueTrue
	if err := s.client.Create(ctx, sa); err != nil {
		return nil, fmt.Errorf("error creating ServiceAccount %q in namespace %q: %w",
			sa.Name, sa.Namespace, err,
		)
	}
	return sa, nil
}

// Delete implements ServiceAccountsDatabase.
func (s *serviceAccountsDatabase) Delete(
	ctx context.Context,
	project string,
	name string,
) error {
	sa := &corev1.ServiceAccount{}
	if err := s.client.Get(
		ctx,
		client.ObjectKey{
			Namespace: project,
			Name:      name,
		},
		sa,
	); err != nil {
		return fmt.Errorf(
			"error getting ServiceAccount %q in namespace %q: %w", name, project, err,
		)
	}
	if !isKargoServiceAccount(sa) {
		return apierrors.NewBadRequest(
			fmt.Sprintf(
				"ServiceAccount %q in namespace %q is not labeled as a Kargo ServiceAccount",
				sa.Name, sa.Namespace,
			),
		)
	}
	if !isKargoManaged(sa) {
		return apierrors.NewBadRequest(
			fmt.Sprintf(
				"ServiceAccount %q in namespace %q is not annotated as Kargo-managed",
				sa.Name, sa.Namespace,
			),
		)
	}

	// Unbind from any Kargo-managed RoleBindings that reference this
	// ServiceAccount.
	rbs := &rbacv1.RoleBindingList{}
	if err := s.client.List(
		ctx,
		rbs,
		client.InNamespace(project),
	); err != nil {
		return fmt.Errorf(
			"error listing RoleBindings in namespace %q: %w",
			project, err,
		)
	}
	for _, rb := range rbs.Items {
		if rb.Annotations[rbacapi.AnnotationKeyManaged] != rbacapi.AnnotationValueTrue {
			continue
		}
		dropServiceAccountFromRoleBinding(&rb, name)
		if err := s.client.Update(ctx, &rb); err != nil {
			return fmt.Errorf(
				"error updating RoleBinding %q in namespace %q: %w",
				name, project, err,
			)
		}
	}

	if err := s.client.Delete(ctx, sa); err != nil {
		return fmt.Errorf(
			"error deleting ServiceAccount %q in namespace %q: %w",
			sa.Name, sa.Namespace, err,
		)
	}
	return nil
}

// Get implements ServiceAccountsDatabase.
func (s *serviceAccountsDatabase) Get(
	ctx context.Context,
	project string,
	name string,
) (*corev1.ServiceAccount, error) {
	sa := &corev1.ServiceAccount{}
	if err := s.client.Get(
		ctx,
		client.ObjectKey{
			Namespace: project,
			Name:      name,
		},
		sa,
	); err != nil {
		return nil, fmt.Errorf(
			"error getting ServiceAccount %q in namespace %q: %w", name, project, err,
		)
	}
	if !isKargoServiceAccount(sa) {
		return nil, apierrors.NewBadRequest(
			fmt.Sprintf(
				"ServiceAccount %q in namespace %q is not labeled as a Kargo ServiceAccount",
				sa.Name, sa.Namespace,
			),
		)
	}
	return sa, nil
}

func (s *serviceAccountsDatabase) GetNewToken(
	ctx context.Context,
	project string,
	name string,
) (string, error) {
	sa, err := s.Get(ctx, project, name)
	if err != nil {
		return "", err
	}
	if !isKargoManaged(sa) {
		return "", apierrors.NewBadRequest(
			fmt.Sprintf(
				"ServiceAccount %q in namespace %q is not annotated as Kargo-managed",
				sa.Name, sa.Namespace,
			),
		)
	}
	tokenSecretName := fmt.Sprintf("%s-token", name)
	tokenSecret := &corev1.Secret{}
	if err := s.client.Get(
		ctx,
		client.ObjectKey{
			Namespace: project,
			Name:      tokenSecretName,
		},
		tokenSecret,
	); err != nil && !apierrors.IsNotFound(err) {
		return "", fmt.Errorf(
			"error getting token Secret for ServiceAccount %q in namespace %q: %w",
			name, project, err,
		)
	}
	if tokenSecret.Name != "" && !isKargoManaged(tokenSecret) {
		return "", apierrors.NewBadRequest(
			fmt.Sprintf(
				"token Secret %q in namespace %q is not annotated as Kargo-managed",
				tokenSecret.Name, tokenSecret.Namespace,
			),
		)
	}
	if tokenSecret.Name != "" {
		if err := s.client.Delete(ctx, tokenSecret); err != nil {
			return "", fmt.Errorf(
				"error deleting existing token Secret for ServiceAccount %q in namespace %q: %w",
				name, project, err,
			)
		}
	}
	tokenSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: project,
			Name:      tokenSecretName,
			Annotations: map[string]string{
				"kubernetes.io/service-account.name": name,
				rbacapi.AnnotationKeyManaged:         rbacapi.AnnotationValueTrue,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	if err := s.client.Create(ctx, tokenSecret); err != nil {
		return "", fmt.Errorf(
			"error creating token Secret for ServiceAccount %q in namespace %q: %w",
			name, project, err,
		)
	}
	// Retrieve Secret -- this is necessary to actually get the token
	tokenSecret = &corev1.Secret{}
	if err := s.client.Get(
		ctx,
		client.ObjectKey{
			Namespace: project,
			Name:      tokenSecretName,
		},
		tokenSecret,
	); err != nil {
		return "", fmt.Errorf(
			"error getting token Secret for ServiceAccount %q in namespace %q: %w",
			name, project, err,
		)
	}
	return string(tokenSecret.Data["token"]), nil
}

// List implements the ServiceAccountsDatabase interface.
func (s *serviceAccountsDatabase) List(
	ctx context.Context,
	project string,
) ([]corev1.ServiceAccount, error) {
	saList := &corev1.ServiceAccountList{}
	if err := s.client.List(
		ctx,
		saList,
		client.InNamespace(project),
		client.MatchingLabels{
			rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
		},
	); err != nil {
		return nil, fmt.Errorf(
			"error listing ServiceAccounts in namespace %q: %w", project, err,
		)
	}
	slices.SortFunc(saList.Items, func(a, b corev1.ServiceAccount) int {
		return strings.Compare(a.Name, b.Name)
	})
	return saList.Items, nil
}

func isKargoServiceAccount(sa *corev1.ServiceAccount) bool {
	return sa.Labels[rbacapi.LabelKeyServiceAccount] == rbacapi.LabelValueTrue
}
