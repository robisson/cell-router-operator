package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
)

type failingClient struct {
	client.Client
	shouldFail func(client.Object) bool
}

func (f *failingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if f.shouldFail != nil && f.shouldFail(obj) {
		return fmt.Errorf("forced create error")
	}
	return f.Client.Create(ctx, obj, opts...)
}

var _ = Describe("Cell Controller", func() {

	const (
		cellName    = "payments"
		serviceName = "payments-entry"
	)

	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(discoveryv1.AddToScheme(scheme)).To(Succeed())
		Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
		Expect(cellv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("reconciles namespace, service, and status", func() {
		cell := &cellv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Name: cellName},
			Spec: cellv1alpha1.CellSpec{
				NamespaceLabels:    map[string]string{"team": "payments"},
				ServiceAnnotations: map[string]string{"example": "true"},
				Entrypoint: cellv1alpha1.CellEntrypointSpec{
					ServiceName: serviceName,
					Port:        8080,
				},
				TearDownOnDelete: true,
			},
		}
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "payments-entry-1",
				Namespace: cellName,
				Labels:    map[string]string{discoveryv1.LabelServiceName: serviceName},
			},
			Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.10"}}},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cell, slice).
			WithStatusSubresource(&cellv1alpha1.Cell{}).
			Build()
		reconciler := &CellReconciler{
			Client:   fakeClient,
			Scheme:   scheme,
			Recorder: record.NewFakeRecorder(32),
		}

		request := reconcile.Request{NamespacedName: types.NamespacedName{Name: cellName}}
		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		namespace := &corev1.Namespace{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, namespace)).To(Succeed())
		Expect(namespace.Labels[constants.ManagedByLabel]).To(Equal(constants.OperatorName))
		Expect(namespace.Labels["team"]).To(Equal("payments"))

		service := &corev1.Service{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: cellName, Name: serviceName}, service)).To(Succeed())
		Expect(service.Labels[constants.CellNameLabel]).To(Equal(cellName))
		Expect(service.Annotations["example"]).To(Equal("true"))
		Expect(service.Spec.Ports).To(HaveLen(1))
		Expect(service.Spec.Ports[0].Port).To(Equal(int32(8080)))

		updated := &cellv1alpha1.Cell{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, updated)).To(Succeed())
		readyCondition := apimeta.FindStatusCondition(updated.Status.Conditions, cellv1alpha1.CellReadyCondition)
		Expect(readyCondition).NotTo(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
		Expect(updated.Status.AvailableEndpoints).To(Equal(int32(1)))

		By("cleaning up managed resources on deletion")
		Expect(fakeClient.Delete(ctx, updated)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		nsCheck := &corev1.Namespace{}
		err = fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, nsCheck)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		cellCheck := &cellv1alpha1.Cell{}
		err = fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, cellCheck)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		// ensure reconciler gracefully handles repeated reconciliation after deletion
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
	})

	It("preserves namespaces when teardown is disabled", func() {
		cell := &cellv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Name: cellName},
			Spec: cellv1alpha1.CellSpec{
				Entrypoint: cellv1alpha1.CellEntrypointSpec{
					ServiceName: serviceName,
					Port:        8080,
				},
				TearDownOnDelete: false,
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cell).
			WithStatusSubresource(&cellv1alpha1.Cell{}).
			Build()

		reconciler := &CellReconciler{Client: fakeClient, Scheme: scheme, Recorder: record.NewFakeRecorder(8)}
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: cellName}}
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		current := &cellv1alpha1.Cell{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, current)).To(Succeed())
		Expect(fakeClient.Delete(ctx, current)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		ns := &corev1.Namespace{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, ns)).To(Succeed())
	})

	It("marks cells as waiting until endpoints are ready", func() {
		cell := &cellv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Name: cellName},
			Spec: cellv1alpha1.CellSpec{
				Entrypoint: cellv1alpha1.CellEntrypointSpec{
					ServiceName: serviceName,
					Port:        8080,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cell).
			WithStatusSubresource(&cellv1alpha1.Cell{}).
			Build()

		reconciler := &CellReconciler{Client: fakeClient, Scheme: scheme, Recorder: record.NewFakeRecorder(8)}
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: cellName}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(cellBackendRequeueDelay))

		updated := &cellv1alpha1.Cell{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: cellName}, updated)).To(Succeed())
		readyCondition := apimeta.FindStatusCondition(updated.Status.Conditions, cellv1alpha1.CellReadyCondition)
		Expect(readyCondition).NotTo(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
	})

	It("ignores namespaces the operator does not manage", func() {
		reconciler := &CellReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
		cell := &cellv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Name: cellName}}

		ns := &corev1.Namespace{}
		ns.Name = "external"
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns).Build()
		reconciler.Client = client

		Expect(reconciler.deleteNamespaceIfManaged(ctx, cell, ns.Name)).To(Succeed())
		Expect(client.Get(ctx, types.NamespacedName{Name: ns.Name}, &corev1.Namespace{})).To(Succeed())
	})

	It("reports namespace reconciliation failures", func() {
		base := &cellv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Name: cellName}, Spec: cellv1alpha1.CellSpec{Entrypoint: cellv1alpha1.CellEntrypointSpec{ServiceName: serviceName, Port: 80}}}
		underlying := fake.NewClientBuilder().WithScheme(scheme).WithObjects(base).WithStatusSubresource(&cellv1alpha1.Cell{}).Build()
		client := &failingClient{
			Client: underlying,
			shouldFail: func(obj client.Object) bool {
				_, isNamespace := obj.(*corev1.Namespace)
				return isNamespace
			},
		}
		reconciler := &CellReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(4)}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: cellName}})
		Expect(err).To(HaveOccurred())

		updated := &cellv1alpha1.Cell{}
		Expect(client.Get(ctx, types.NamespacedName{Name: cellName}, updated)).To(Succeed())
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, cellv1alpha1.CellNamespaceReadyCondition)
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
	})

	It("reports service reconciliation failures", func() {
		base := &cellv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Name: cellName}, Spec: cellv1alpha1.CellSpec{Entrypoint: cellv1alpha1.CellEntrypointSpec{ServiceName: serviceName, Port: 80}}}
		underlying := fake.NewClientBuilder().WithScheme(scheme).WithObjects(base).WithStatusSubresource(&cellv1alpha1.Cell{}).Build()
		client := &failingClient{
			Client: underlying,
			shouldFail: func(obj client.Object) bool {
				_, isService := obj.(*corev1.Service)
				return isService
			},
		}
		reconciler := &CellReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(4)}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: cellName}})
		Expect(err).To(HaveOccurred())

		updated := &cellv1alpha1.Cell{}
		Expect(client.Get(ctx, types.NamespacedName{Name: cellName}, updated)).To(Succeed())
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, cellv1alpha1.CellServiceReadyCondition)
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
	})

	It("does not delete services when not owned by the cell", func() {
		cell := &cellv1alpha1.Cell{ObjectMeta: metav1.ObjectMeta{Name: cellName}, Spec: cellv1alpha1.CellSpec{Entrypoint: cellv1alpha1.CellEntrypointSpec{ServiceName: serviceName}}}
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: cellName}}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
		reconciler := &CellReconciler{Client: fakeClient, Scheme: scheme}

		Expect(reconciler.deleteEntrypointService(ctx, cell, cellName)).To(Succeed())
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: cellName, Name: serviceName}, &corev1.Service{})).To(Succeed())
	})
})
