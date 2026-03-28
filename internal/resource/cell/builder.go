package cell

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	"github.com/robisson/cell-router-operator/internal/utils/metadata"
)

const portNameTemplate = "port-%d"

// MutateNamespace applies the desired state for the cell namespace into the provided object.
func MutateNamespace(ns *corev1.Namespace, cell *cellv1alpha1.Cell) {
	ns.Labels = metadata.Merge(ns.Labels, map[string]string{
		constants.ManagedByLabel: constants.OperatorName,
		constants.CellNameLabel:  cell.Name,
	})
	ns.Annotations = metadata.Merge(ns.Annotations, cell.Spec.NamespaceAnnotations)
	ns.Labels = metadata.Merge(ns.Labels, cell.Spec.NamespaceLabels, constants.ManagedByLabel, constants.CellNameLabel)
}

// MutateService applies the desired state for the cell entrypoint service into the provided object.
func MutateService(svc *corev1.Service, cell *cellv1alpha1.Cell) {
	svc.Labels = metadata.Merge(svc.Labels, map[string]string{
		constants.ManagedByLabel:         constants.OperatorName,
		constants.CellNameLabel:          cell.Name,
		constants.EntrypointServiceLabel: cell.Spec.Entrypoint.ServiceName,
	})
	svc.Annotations = metadata.Merge(svc.Annotations, cell.Spec.ServiceAnnotations)
	svc.Labels = metadata.Merge(svc.Labels, cell.Spec.ServiceLabels,
		constants.ManagedByLabel,
		constants.CellNameLabel,
		constants.EntrypointServiceLabel,
	)

	if svc.Spec.Selector == nil {
		svc.Spec.Selector = map[string]string{}
	} else {
		for k := range svc.Spec.Selector {
			delete(svc.Spec.Selector, k)
		}
	}

	if len(cell.Spec.WorkloadSelector) > 0 {
		for k, v := range cell.Spec.WorkloadSelector {
			svc.Spec.Selector[k] = v
		}
	} else {
		svc.Spec.Selector[constants.CellNameLabel] = cell.Name
	}

	serviceType := cell.Spec.Entrypoint.Type
	if serviceType == "" {
		serviceType = corev1.ServiceTypeClusterIP
	}
	svc.Spec.Type = serviceType

	protocol := cell.Spec.Entrypoint.Protocol
	if protocol == "" {
		protocol = corev1.ProtocolTCP
	}

	targetPort := cell.Spec.Entrypoint.Port
	if cell.Spec.Entrypoint.TargetPort != nil {
		targetPort = *cell.Spec.Entrypoint.TargetPort
	}

	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       fmt.Sprintf(portNameTemplate, cell.Spec.Entrypoint.Port),
			Port:       cell.Spec.Entrypoint.Port,
			TargetPort: intstr.FromInt(int(targetPort)),
			Protocol:   protocol,
		},
	}
}
