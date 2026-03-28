package cell

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
)

func TestMutateNamespace(t *testing.T) {
	cell := &cellv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Name: "payments"},
		Spec: cellv1alpha1.CellSpec{
			NamespaceLabels:      map[string]string{"env": "prod"},
			NamespaceAnnotations: map[string]string{"owner": "sre"},
		},
	}

	ns := &corev1.Namespace{}
	MutateNamespace(ns, cell)

	if ns.Labels[constants.ManagedByLabel] != constants.OperatorName {
		t.Fatalf("expected managed-by label to be %q", constants.OperatorName)
	}
	if ns.Labels["env"] != "prod" {
		t.Fatalf("expected namespace label env=prod, got %q", ns.Labels["env"])
	}
	if ns.Annotations["owner"] != "sre" {
		t.Fatalf("expected namespace annotation owner=sre, got %q", ns.Annotations["owner"])
	}
}

func TestMutateService(t *testing.T) {
	port := int32(9090)
	cell := &cellv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Name: "payments"},
		Spec: cellv1alpha1.CellSpec{
			WorkloadSelector:   map[string]string{"app": "checkout"},
			ServiceAnnotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-type": "nlb"},
			ServiceLabels:      map[string]string{"tier": "frontend"},
			Entrypoint: cellv1alpha1.CellEntrypointSpec{
				ServiceName: "entry",
				Port:        8080,
				TargetPort:  &port,
				Protocol:    corev1.ProtocolTCP,
				Type:        corev1.ServiceTypeNodePort,
			},
		},
	}

	svc := &corev1.Service{}
	MutateService(svc, cell)

	if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Fatalf("expected service type NodePort, got %s", svc.Spec.Type)
	}
	if svc.Spec.Selector["app"] != "checkout" {
		t.Fatalf("expected selector app=checkout, got %v", svc.Spec.Selector)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected one port, got %d", len(svc.Spec.Ports))
	}
	if svc.Spec.Ports[0].TargetPort.IntValue() != int(port) {
		t.Fatalf("expected targetPort=%d, got %d", port, svc.Spec.Ports[0].TargetPort.IntValue())
	}
	if svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] != "nlb" {
		t.Fatalf("expected annotation propagated")
	}
	if svc.Labels["tier"] != "frontend" {
		t.Fatalf("expected custom label persisted")
	}
}
