package ui

// Column policy: which column gives up cells first, and which one leaves the
// screen first. Both answers are derived from the header string, because that
// is the identifier the rest of the table already keys on — the minimum-width
// lookup in tryFit, the identity-column lookup in tableBody, and the metric
// columns in trend.go all do the same thing. A fourth lookup beside them is
// cheaper than turning Cols []string into a struct, which would touch thirty
// kind literals in internal/k8s/kinds.go, every positional row builder, and
// all of internal/mock.
//
// The two are separate questions and were previously answered by the same
// accident of position:
//
//   - weight decides who shrinks. The old loop took cells from the widest
//     column over its minimum, which is always NAME, so at 100 columns NAME
//     was already `api-gateway-7d9f4…` while four columns were still on
//     screen. Two pods differing only in their hash suffix became one row.
//   - priority decides who is dropped. The old loop dropped the rightmost
//     column, which is not a statement about importance: AGE sits last in the
//     pod columns and died first at every width, while IMAGE and NODE — which
//     nobody reads first during an incident — outlived it.

// identityMaxMin caps how much the identity column may demand before other
// columns start being dropped for it.
//
// Thirty cells covers a Deployment-generated pod name — `billing-worker-`
// plus a ten-character ReplicaSet hash and a five-character suffix — which is
// the longest name an operator reads routinely. Past that the tail is
// entropy, and demanding all of it would clear the table of every other
// column in order to show a hash.
const identityMaxMin = 30

// colWeight scales a column's appetite for cells. The shrink loop takes from
// whichever column has the largest natural-width-per-unit-weight, so a weight
// of 3 means "this column gives up one cell for every three the others give
// up".
//
// Identity columns get the most, because a truncated name is not a name: it
// is two different pods rendered identically.
func colWeight(header string) int {
	switch header {
	case "NAME", "OBJECT":
		return 3
	case "STATUS", "READY", "PHASE", "HEALTH", "SYNC":
		return 2
	default:
		return 1
	}
}

// colPriority decides drop order: the lowest-priority column leaves first.
//
// The identity column sits above everything and is never dropped — dropIndex
// refuses to take the first column at all, so the rule does not rely on
// fitCols' two-column floor to hold.
func colPriority(header string) int {
	switch header {
	case "NAME", "OBJECT":
		return 100
	case "STATUS", "PHASE", "HEALTH", "SYNC":
		return 90
	case "AGE":
		return 80
	case "READY", "RESTARTS":
		return 70
	case "NAMESPACE":
		return 60
	default:
		return 50
	}
}

// dropIndex picks the position in keep whose column should leave the screen
// first: lowest priority, and on a tie the rightmost of those, so columns the
// policy has no opinion about still go right to left as they always did.
func dropIndex(cols []string, keep []int) int {
	worst, worstPrio := -1, 0
	for k, ci := range keep {
		// Never the first column: it carries the row's identity, and a table
		// of unnamed rows is not a table.
		if k == 0 {
			continue
		}
		if p := colPriority(cols[ci]); worst < 0 || p <= worstPrio {
			worst, worstPrio = k, p
		}
	}
	return worst
}
