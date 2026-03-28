package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

var _ = Describe("CellRouter Controller", func() {
	const (
		routerName  = "global-router"
		cellName    = "payments"
		gatewayName = "edge-gw"
		gatewayNS   = "cell-routing"
		routeName   = "payments-route"
		staleRoute  = "stale-route"
	)

	var (
		scheme         *runtime.Scheme
		ctx            context.Context
		baseCell       *cellv1alpha1.Cell
		baseRouter     *cellv1alpha1.CellRouter
		baseStaleRoute *gatewayv1.HTTPRoute
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(cellv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(gatewayv1.AddToScheme(scheme)).To(Succeed())
		Expect(gatewayv1beta1.AddToScheme(scheme)).To(Succeed())

		readyCondition := metav1.Condition{Type: cellv1alpha1.CellReadyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now()}
		baseCell = &cellv1alpha1.Cell{
			ObjectMeta: metav1.ObjectMeta{Name: cellName},
			Spec: cellv1alpha1.CellSpec{
				Entrypoint: cellv1alpha1.CellEntrypointSpec{
					ServiceName: "entry",
					Port:        8080,
				},
			},
			Status: cellv1alpha1.CellStatus{
				Namespace:          cellName,
				EntrypointService:  cellName + "/entry",
				Conditions:         []metav1.Condition{readyCondition},
				ObservedGeneration: 1,
			},
		}

		baseRouter = &cellv1alpha1.CellRouter{
			ObjectMeta: metav1.ObjectMeta{Name: routerName},
			Spec: cellv1alpha1.CellRouterSpec{
				Gateway: cellv1alpha1.CellGatewaySpec{
					Name:             gatewayName,
					Namespace:        gatewayNS,
					GatewayClassName: "istio",
					Listeners: []cellv1alpha1.CellGatewayListener{{
						Name:     "http",
						Port:     80,
						Protocol: gatewayv1.HTTPProtocolType,
					}},
				},
				Routes: []cellv1alpha1.CellRouteSpec{{
					Name:          routeName,
					CellRef:       cellName,
					ListenerNames: []string{"http"},
					Hostnames:     []gatewayv1.Hostname{"api.example.com"},
				}},
			},
		}

		baseStaleRoute = &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      staleRoute,
				Namespace: gatewayNS,
				Labels: map[string]string{
					constants.ManagedByLabel:  constants.OperatorName,
					constants.RouterNameLabel: routerName,
				},
			},
		}
	})

	It("creates gateway and routes, trims stale resources, and updates status", func() {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(baseCell.DeepCopy(), baseRouter.DeepCopy(), baseStaleRoute.DeepCopy()).
			WithStatusSubresource(&cellv1alpha1.Cell{}, &cellv1alpha1.CellRouter{}, &gatewayv1.HTTPRoute{}, &gatewayv1.Gateway{}, &gatewayv1beta1.ReferenceGrant{}).
			Build()
		reconciler := &CellRouterReconciler{
			Client:   fakeClient,
			Scheme:   scheme,
			Recorder: record.NewFakeRecorder(64),
		}

		request := reconcile.Request{NamespacedName: types.NamespacedName{Name: routerName}}
		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(requeueDelay))

		gateway := &gatewayv1.Gateway{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: gatewayNS, Name: gatewayName}, gateway)).To(Succeed())
		Expect(gateway.Spec.GatewayClassName).To(Equal(gatewayv1.ObjectName("istio")))

		namespace := &corev1.Namespace{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: gatewayNS}, namespace)).To(Succeed())
		Expect(namespace.Labels[constants.RouterNameLabel]).To(Equal(routerName))

		grant := &gatewayv1beta1.ReferenceGrant{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: cellName, Name: routeName + "-backend"}, grant)).To(Succeed())
		Expect(grant.Spec.From).To(HaveLen(1))

		httpRoute := &gatewayv1.HTTPRoute{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: gatewayNS, Name: routeName}, httpRoute)).To(Succeed())
		Expect(httpRoute.Labels[constants.CellNameLabel]).To(Equal(cellName))
		Expect(httpRoute.Spec.Rules).To(HaveLen(1))

		stale := &gatewayv1.HTTPRoute{}
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: gatewayNS, Name: staleRoute}, stale)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		updatedRouter := &cellv1alpha1.CellRouter{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: routerName}, updatedRouter)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(updatedRouter, constants.FinalizerCellRouter)).To(BeTrue())
		condition := apimeta.FindStatusCondition(updatedRouter.Status.Conditions, cellv1alpha1.CellRouterRoutesReadyCondition)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))

		By("marking routes as ready and reconciling again")
		controllerName := gatewayv1.GatewayController("cellrouter.io/controller")
		parentNamespace := gatewayv1.Namespace(gatewayNS)
		httpRoute.Status.Parents = []gatewayv1.RouteParentStatus{{
			ParentRef: gatewayv1.ParentReference{
				Name:      gatewayv1.ObjectName(gatewayName),
				Namespace: &parentNamespace,
			},
			ControllerName: controllerName,
			Conditions: []metav1.Condition{
				{
					Type:               string(gatewayv1.RouteConditionAccepted),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               string(gatewayv1.RouteConditionResolvedRefs),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
			},
		}}
		Expect(fakeClient.Status().Update(ctx, httpRoute)).To(Succeed())

		result, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: routerName}, updatedRouter)).To(Succeed())
		ready := apimeta.FindStatusCondition(updatedRouter.Status.Conditions, cellv1alpha1.CellRouterReadyCondition)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
	})

	It("cleans up managed resources during deletion", func() {
		dyingRouter := baseRouter.DeepCopy()
		dyingRouter.Finalizers = []string{constants.FinalizerCellRouter}
		dyingRouter.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		grant := &gatewayv1beta1.ReferenceGrant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      routeName + "-backend",
				Namespace: cellName,
				Labels: map[string]string{
					constants.ManagedByLabel:  constants.OperatorName,
					constants.RouterNameLabel: routerName,
				},
			},
		}
		Expect(controllerutil.SetControllerReference(dyingRouter, grant, scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(baseCell.DeepCopy(), dyingRouter, baseStaleRoute.DeepCopy(), grant).
			WithStatusSubresource(&cellv1alpha1.Cell{}, &cellv1alpha1.CellRouter{}, &gatewayv1.HTTPRoute{}, &gatewayv1.Gateway{}, &gatewayv1beta1.ReferenceGrant{}).
			Build()
		reconciler := &CellRouterReconciler{
			Client:   fakeClient,
			Scheme:   scheme,
			Recorder: record.NewFakeRecorder(32),
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: routerName}})
		Expect(err).NotTo(HaveOccurred())

		httpRoute := &gatewayv1.HTTPRoute{}
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: gatewayNS, Name: routeName}, httpRoute)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		gateway := &gatewayv1.Gateway{}
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: gatewayNS, Name: gatewayName}, gateway)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		grantCheck := &gatewayv1beta1.ReferenceGrant{}
		err = fakeClient.Get(ctx, types.NamespacedName{Namespace: cellName, Name: routeName + "-backend"}, grantCheck)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		routerCheck := &cellv1alpha1.CellRouter{}
		err = fakeClient.Get(ctx, types.NamespacedName{Name: routerName}, routerCheck)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("ignores gateways managed by other controllers", func() {
		gateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: gatewayName, Namespace: gatewayNS}}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(baseCell.DeepCopy(), baseRouter.DeepCopy(), gateway).
			Build()
		reconciler := &CellRouterReconciler{Client: fakeClient, Scheme: scheme}

		Expect(reconciler.deleteManagedGateway(ctx, baseRouter)).To(Succeed())
		gwCheck := &gatewayv1.Gateway{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: gatewayName, Namespace: gatewayNS}, gwCheck)).To(Succeed())
	})

	It("keeps declared routes when they are still expected", func() {
		existing := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      routeName,
				Namespace: gatewayNS,
				Labels: map[string]string{
					constants.ManagedByLabel:  constants.OperatorName,
					constants.RouterNameLabel: routerName,
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existing).
			Build()
		reconciler := &CellRouterReconciler{Client: fakeClient, Scheme: scheme}

		expected := map[string]struct{}{routeName: {}}
		Expect(reconciler.cleanupStaleRoutes(ctx, baseRouter, gatewayNS, expected)).To(Succeed())
		Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: gatewayNS, Name: routeName}, &gatewayv1.HTTPRoute{})).To(Succeed())
	})

	It("treats routes with unresolved backend references as not ready", func() {
		controllerName := gatewayv1.GatewayController("cellrouter.io/controller")
		namespace := gatewayv1.Namespace(gatewayNS)
		httpRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: gatewayNS},
			Status: gatewayv1.HTTPRouteStatus{
				RouteStatus: gatewayv1.RouteStatus{
					Parents: []gatewayv1.RouteParentStatus{
						{
							ParentRef: gatewayv1.ParentReference{
								Name:      gatewayv1.ObjectName(gatewayName),
								Namespace: &namespace,
							},
							ControllerName: controllerName,
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayv1.RouteConditionAccepted),
									Status:             metav1.ConditionTrue,
									LastTransitionTime: metav1.Now(),
								},
								{
									Type:               string(gatewayv1.RouteConditionResolvedRefs),
									Status:             metav1.ConditionFalse,
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			},
		}

		ready, readyTime := isRouteReady(httpRoute, gatewayName, gatewayNS)
		Expect(ready).To(BeFalse())
		Expect(readyTime).To(BeNil())
	})
})
