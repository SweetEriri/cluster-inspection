package testhelper

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestService(t *testing.T) {
	client := CreateFakeK8sClient(t)
	ports := []corev1.ServicePort{
		{
			Name:     "test1 - 1",
			Port:     80,
			Protocol: "TCP",
		},
	}

	// 测试创建Service
	svc, _ := CreateFakeService(t, client, "test1", "test", "10.0.0.1", ports, context.Background(), metav1.CreateOptions{})
	AssertNotNil(t, svc, "测试创建Service是否成功")
	AssertEq(t, ports[0].Port, svc.Spec.Ports[0].Port, "验证创建的Service是否有问题")

	ports = []corev1.ServicePort{
		{
			Name:     "test1 - 1",
			Port:     443,
			Protocol: "TCP",
			NodePort: 10086,
		},
	}

	// 测试更新Services
	originalSvc := svc.DeepCopy()

	updateSvc, _ := UpdateFakeService(t, client, svc, ports, corev1.ServiceTypeNodePort, context.Background(), metav1.UpdateOptions{})
	AssertNotNil(t, updateSvc, "测试更新Service是否成功")
	AssertEq(t, ports[0].Port, updateSvc.Spec.Ports[0].Port, "验证更新的Services是否有问题")
	AssertNotEq(t, originalSvc.Spec.Ports[0].Port, svc.Spec.Ports[0].Port, "验证更新时Service是否会收到影响")

	// 测试删除Service
	err := DeleteFakeService(t, client, svc.Name, svc.Namespace, context.Background(), metav1.DeleteOptions{})
	AssertNil(t, err, "验证删除Service的操作")
}
