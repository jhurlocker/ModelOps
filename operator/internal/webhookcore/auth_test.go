package webhookcore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	return s
}

func TestBuildAuthHeader_Success(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-secret", Namespace: "ns-1"},
		Data:       map[string][]byte{"token": []byte("my-token-value")},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(secret).Build()

	header, err := BuildAuthHeader(context.Background(), c, "ns-1", SecretKeyRef{Name: "auth-secret", Key: "token"}, "Bearer ")
	require.NoError(t, err)
	require.Equal(t, "Bearer my-token-value", header)
}

func TestBuildAuthHeader_MissingSecret(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	_, err := BuildAuthHeader(context.Background(), c, "ns-1", SecretKeyRef{Name: "auth-secret", Key: "token"}, "Bearer ")
	require.Error(t, err)
}

func TestBuildAuthHeader_MissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-secret", Namespace: "ns-1"},
		Data:       map[string][]byte{"different-key": []byte("val")},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(secret).Build()
	_, err := BuildAuthHeader(context.Background(), c, "ns-1", SecretKeyRef{Name: "auth-secret", Key: "token"}, "Bearer ")
	require.Error(t, err)
}

func TestBuildAuthHeader_NoSchemePrefix(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-secret", Namespace: "ns-1"},
		Data:       map[string][]byte{"api-key": []byte("key-12345")},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(secret).Build()

	header, err := BuildAuthHeader(context.Background(), c, "ns-1", SecretKeyRef{Name: "auth-secret", Key: "api-key"}, "")
	require.NoError(t, err)
	require.Equal(t, "key-12345", header)
}
