package constants

const (
	// OperatorName is used for labels and logging.
	OperatorName = "cell-router-operator"

	// ManagedByLabel marks Kubernetes resources managed by the operator.
	ManagedByLabel = "cellrouter.io/managed-by"

	// CellNameLabel stores the cell name associated with a resource.
	CellNameLabel = "cellrouter.io/cell-name"

	// EntrypointServiceLabel stores the entrypoint service name.
	EntrypointServiceLabel = "cellrouter.io/entrypoint-service"

	// RouterNameLabel stores the router resource responsible for a route.
	RouterNameLabel = "cellrouter.io/router-name"

	// FinalizerCell ensures resources created for a Cell are cleaned up.
	FinalizerCell = "cell.cellrouter.io/finalizer"

	// FinalizerCellRouter ensures resources created for a CellRouter are cleaned up.
	FinalizerCellRouter = "cellrouter.cellrouter.io/finalizer"
)
