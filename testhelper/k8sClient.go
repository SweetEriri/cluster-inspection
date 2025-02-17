package testhelper

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// CreateFakeK8sClient 创建一个k8s模拟客户端
func CreateFakeK8sClient(t *testing.T) kubernetes.Interface {
	t.Helper()
	return fake.NewClientset()
}

// CreateFakeService 创建并模拟一个service
func CreateFakeService(
	t *testing.T,
	client kubernetes.Interface,
	name, namespace, ip string,
	ports []corev1.ServicePort,
	ctx context.Context,
	options metav1.CreateOptions) (*corev1.Service, error) {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports:     ports,
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: ip,
		},
	}
	return client.CoreV1().Services(namespace).Create(ctx, svc, options)
}

// DeleteFakeService 删除service资源
func DeleteFakeService(
	t *testing.T,
	client kubernetes.Interface,
	name, namespace string,
	ctx context.Context,
	options metav1.DeleteOptions,
) error {
	t.Helper()
	return client.CoreV1().Services(namespace).Delete(ctx, name, options)
}

// UpdateFakeService 更新service资源
func UpdateFakeService(
	t *testing.T,
	client kubernetes.Interface,
	svc *corev1.Service,
	ports []corev1.ServicePort,
	svcType corev1.ServiceType,
	ctx context.Context,
	options metav1.UpdateOptions,
) (*corev1.Service, error) {
	t.Helper()
	if ports != nil {
		svc.Spec.Ports = ports
	}
	if svcType != svc.Spec.Type {
		svc.Spec.Type = svcType
	}
	return client.CoreV1().Services(svc.GetNamespace()).Update(ctx, svc, options)
}
