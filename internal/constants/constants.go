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

	// PlacementNameLabel stores the placement resource responsible for a route.
	PlacementNameLabel = "cellrouter.io/placement-name"

	// FinalizerCell ensures resources created for a Cell are cleaned up.
	FinalizerCell = "cell.cellrouter.io/finalizer"

	// FinalizerCellRouter ensures resources created for a CellRouter are cleaned up.
	FinalizerCellRouter = "cellrouter.cellrouter.io/finalizer"

	// CellResourceQuotaName is the fixed name of the quota managed for a cell.
	CellResourceQuotaName = "cell-quota"
	// CellLimitRangeName is the fixed name of the limit range managed for a cell.
	CellLimitRangeName = "cell-limits"
	// CellNetworkPolicyName is the fixed name of the network policy managed for a cell.
	CellNetworkPolicyName = "cell-entrypoint"
)
