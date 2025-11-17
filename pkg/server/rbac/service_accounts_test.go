package rbac

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rbacapi "github.com/akuity/kargo/api/rbac/v1alpha1"
)

const testServiceAccountName = "fake-service-account"

func Test_serviceAccountsDatabase_Create(t *testing.T) {
	t.Run("ServiceAccount already exists", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}},
		).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			Create(t.Context(), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}})
		require.Error(t, err)
		require.True(t, apierrors.IsAlreadyExists(err))
	})

	t.Run("success", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		sa, err := NewKubernetesServiceAccountsDatabase(c).
			Create(t.Context(), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}})
		require.NoError(t, err)
		require.NotNil(t, sa)
		require.Equal(t, testProject, sa.Namespace)
		require.Equal(t, testServiceAccountName, sa.Name)
		require.Equal(
			t,
			rbacapi.LabelValueTrue,
			sa.Labels[rbacapi.LabelKeyServiceAccount],
		)
		require.Equal(
			t,
			rbacapi.AnnotationValueTrue,
			sa.Annotations[rbacapi.AnnotationKeyManaged],
		)
		sa = &corev1.ServiceAccount{}
		err = c.Get(
			t.Context(),
			client.ObjectKey{
				Namespace: testProject,
				Name:      testServiceAccountName,
			},
			sa,
		)
		require.NoError(t, err)
		require.NotNil(t, sa)
		require.Equal(t, testProject, sa.Namespace)
		require.Equal(t, testServiceAccountName, sa.Name)
		require.Equal(
			t,
			rbacapi.LabelValueTrue,
			sa.Labels[rbacapi.LabelKeyServiceAccount],
		)
		require.Equal(
			t,
			rbacapi.AnnotationValueTrue,
			sa.Annotations[rbacapi.AnnotationKeyManaged],
		)
	})
}

func Test_serviceAccountsDatabase_Delete(t *testing.T) {
	t.Run("ServiceAccount not found", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		err := NewKubernetesServiceAccountsDatabase(c).
			Delete(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsNotFound(err))
	})

	t.Run("ServiceAccount not labeled as Kargo ServiceAccount", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}},
		).Build()
		err := NewKubernetesServiceAccountsDatabase(c).
			Delete(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsBadRequest(err))
	})

	t.Run("ServiceAccount not annotated as Kargo-managed", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
			}},
		).Build()
		err := NewKubernetesServiceAccountsDatabase(c).
			Delete(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsBadRequest(err))
	})

	t.Run("success", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
				Annotations: map[string]string{
					rbacapi.AnnotationKeyManaged: rbacapi.AnnotationValueTrue,
				},
			}},
			// This RoleBinding is Kargo-managed and should be modified.
			&rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testProject,
					Name:      testServiceAccountName,
					Annotations: map[string]string{
						rbacapi.AnnotationKeyManaged: rbacapi.AnnotationValueTrue,
					},
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Namespace: testProject,
						Name:      testServiceAccountName,
					},
					{
						// This should be left alone because it doesn't reference the SA
						// that's being deleted.
						Kind:      "ServiceAccount",
						Namespace: testProject,
						Name:      "other-service-account",
					},
				},
			},
			// This RoleBinding is not Kargo-managed and should not be modified.
			&rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testProject,
					Name:      "other-role-binding",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Namespace: testProject,
						Name:      testServiceAccountName,
					},
					{
						Kind:      "ServiceAccount",
						Namespace: testProject,
						Name:      "other-service-account",
					},
				},
			},
		).Build()
		err := NewKubernetesServiceAccountsDatabase(c).
			Delete(t.Context(), testProject, testServiceAccountName)
		require.NoError(t, err)
		rb := &rbacv1.RoleBinding{}
		err = c.Get(
			t.Context(),
			client.ObjectKey{
				Namespace: testProject,
				Name:      testServiceAccountName,
			},
			rb,
		)
		require.NoError(t, err)
		// Subjects should be modified because this RoleBinding is Kargo- managed.
		require.Equal(
			t,
			[]rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Namespace: testProject,
				Name:      "other-service-account",
			}},
			rb.Subjects,
		)
		rb = &rbacv1.RoleBinding{}
		err = c.Get(
			t.Context(),
			client.ObjectKey{
				Namespace: testProject,
				Name:      "other-role-binding",
			},
			rb,
		)
		require.NoError(t, err)
		// Subjects should be untouched because this RoleBinding isn't Kargo-
		// managed.
		require.Equal(
			t,
			[]rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Namespace: testProject,
					Name:      testServiceAccountName,
				},
				{
					Kind:      "ServiceAccount",
					Namespace: testProject,
					Name:      "other-service-account",
				},
			},
			rb.Subjects,
		)
		sa := &corev1.ServiceAccount{}
		err = c.Get(
			t.Context(),
			client.ObjectKey{
				Namespace: testProject,
				Name:      testServiceAccountName,
			},
			sa,
		)
		require.Error(t, err)
		require.True(t, apierrors.IsNotFound(err))
	})
}

func Test_serviceAccountsDatabase_Get(t *testing.T) {
	t.Run("ServiceAccount not found", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			Get(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsNotFound(err))
	})

	t.Run("ServiceAccount not labeled as Kargo ServiceAccount", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}},
		).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			Get(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsBadRequest(err))
	})

	t.Run("success", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
			}},
		).Build()
		sa, err := NewKubernetesServiceAccountsDatabase(c).
			Get(t.Context(), testProject, testServiceAccountName)
		require.NoError(t, err)
		require.NotNil(t, sa)
		require.Equal(t, testServiceAccountName, sa.Name)
		require.Equal(t, testProject, sa.Namespace)
	})
}

func Test_serviceAccountsDatabase_GetNewToken(t *testing.T) {
	t.Run("ServiceAccount not found", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			GetNewToken(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsNotFound(err))
	})

	t.Run("ServiceAccount not labeled as Kargo ServiceAccount", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}}).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			GetNewToken(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsBadRequest(err))
	})

	t.Run("ServiceAccount not annotated as Kargo-managed", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
			}},
		).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			GetNewToken(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsBadRequest(err))
	})

	t.Run("success with no existing token", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
				Annotations: map[string]string{
					rbacapi.AnnotationKeyManaged: rbacapi.AnnotationValueTrue,
				},
			}},
		).Build()
		token, err := NewKubernetesServiceAccountsDatabase(c).
			GetNewToken(t.Context(), testProject, testServiceAccountName)
		require.NoError(t, err)
		// A token created by the fake client will be empty
		require.Empty(t, token)
		secret := &corev1.Secret{}
		err = c.Get(
			t.Context(),
			client.ObjectKey{
				Namespace: testProject,
				Name:      testServiceAccountName + "-token",
			},
			secret,
		)
		require.NoError(t, err)
		require.Equal(t, corev1.SecretTypeServiceAccountToken, secret.Type)
		require.Equal(
			t,
			rbacapi.AnnotationValueTrue,
			secret.Annotations[rbacapi.AnnotationKeyManaged],
		)
		require.Equal(
			t,
			testServiceAccountName,
			secret.Annotations["kubernetes.io/service-account.name"],
		)
	})

	t.Run("existing token is not annotated as Kargo-managed", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
				Annotations: map[string]string{
					rbacapi.AnnotationKeyManaged: rbacapi.AnnotationValueTrue,
				},
			}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName + "-token",
			}},
		).Build()
		_, err := NewKubernetesServiceAccountsDatabase(c).
			GetNewToken(t.Context(), testProject, testServiceAccountName)
		require.Error(t, err)
		require.True(t, apierrors.IsBadRequest(err))
	})

	t.Run("success with existing token replacement", func(t *testing.T) {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
				Labels: map[string]string{
					rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
				},
				Annotations: map[string]string{
					rbacapi.AnnotationKeyManaged: rbacapi.AnnotationValueTrue,
				},
			},
		}
		existingSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName + "-token",
				Annotations: map[string]string{
					rbacapi.AnnotationKeyManaged: rbacapi.AnnotationValueTrue,
				},
			},
			Type: corev1.SecretTypeServiceAccountToken,
			Data: map[string][]byte{"token": []byte("old-token")},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sa, existingSecret).Build()
		token, err := NewKubernetesServiceAccountsDatabase(c).
			GetNewToken(t.Context(), testProject, testServiceAccountName)
		require.NoError(t, err)
		require.Empty(t, token)
		secret := &corev1.Secret{}
		err = c.Get(
			t.Context(),
			client.ObjectKey{
				Namespace: testProject,
				Name:      testServiceAccountName + "-token",
			},
			secret,
		)
		require.NoError(t, err)
		require.Equal(t, corev1.SecretTypeServiceAccountToken, secret.Type)
		require.Equal(
			t,
			testServiceAccountName,
			secret.Annotations["kubernetes.io/service-account.name"],
		)
		// A token created by the fake client will be empty, but we can verify the
		// old token was deleted
		require.Empty(t, secret.Data["token"])
	})
}

func Test_serviceAccountsDatabase_List(t *testing.T) {
	t.Run("no ServiceAccounts", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		saList, err := NewKubernetesServiceAccountsDatabase(c).
			List(t.Context(), testProject)
		require.NoError(t, err)
		require.Empty(t, saList)
	})

	t.Run("with non-Kargo ServiceAccounts", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}},
		).Build()
		saList, err := NewKubernetesServiceAccountsDatabase(c).
			List(t.Context(), testProject)
		require.NoError(t, err)
		require.Empty(t, saList)
	})

	t.Run("with Kargo ServiceAccounts", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testProject,
					Name:      "test-sa-1",
					Labels: map[string]string{
						rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
					},
				},
			},
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testProject,
					Name:      "test-sa-2",
					Labels: map[string]string{
						rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
					},
				},
			},
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testProject,
					Name:      "test-sa-3",
				}, // Not labeled as Kargo ServiceAccount
			},
		).Build()
		saList, err := NewKubernetesServiceAccountsDatabase(c).
			List(t.Context(), testProject)
		require.NoError(t, err)
		require.Len(t, saList, 2)

		names := []string{saList[0].Name, saList[1].Name}
		require.Contains(t, names, "test-sa-1")
		require.Contains(t, names, "test-sa-2")
	})
}

func Test_isKargoServiceAccount(t *testing.T) {
	t.Run("not a Kargo ServiceAccount", func(t *testing.T) {
		require.False(t, isKargoServiceAccount(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: testProject,
				Name:      testServiceAccountName,
			}},
		))
	})

	t.Run("is a Kargo ServiceAccount", func(t *testing.T) {
		require.True(t, isKargoServiceAccount(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sa",
					Labels: map[string]string{
						rbacapi.LabelKeyServiceAccount: rbacapi.LabelValueTrue,
					},
				},
			},
		))
	})

	t.Run("has label but wrong value", func(t *testing.T) {
		require.False(t, isKargoServiceAccount(
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sa",
					Labels: map[string]string{
						rbacapi.LabelKeyServiceAccount: "false",
					},
				},
			},
		))
	})
}
