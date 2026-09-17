// Package domain defines the contract between the UI (internal/ui) and a
// cluster backend — either the real one (internal/k8s) or the offline demo
// (internal/mock). Neither backend package depends on the other; both depend
// only on this package, and the UI depends only on this package too.
package domain

import (
	"errors"
	"io"
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// AllNamespaces is the :ns sentinel that shows every namespace at once.
const AllNamespaces = "all"

// DemoContext is the context name that selects k10s's built-in demo backend
// (internal/mock) instead of a cluster. It is a context, not a mode, so it
// travels the one path everything else already uses: `:ctx`, `/demo` and
// `k10s demo` all just ask to connect to this name, and leaving the demo is
// picking any other context.
//
// It lives here because it is the one string the UI and main.go must agree
// on, and neither may import the other's backend. The UI never constructs a
// demo backend; it only ever names this context.
const DemoContext = "k10s-demo"

// IsDemoContext reports whether name addresses the demo backend. The demo
// serves several contexts of its own so that switching between them is
// demonstrable, and they all carry the prefix — a context that says "demo"
// wherever it is displayed, which is the point: no frame of the demo should
// read like somebody's real cluster.
func IsDemoContext(name string) bool {
	return name == DemoContext || strings.HasPrefix(name, DemoContext+"-")
}

// ShellSession is a live exec stream: write keystrokes to it, read the
// program's output off Output, and tell it when the panel resizes.
type ShellSession interface {
	io.Writer
	// Output carries raw terminal bytes, escape sequences included — the
	// caller feeds them to a terminal emulator.
	Output() <-chan []byte
	Resize(cols, rows int)
	Close() error
}

// ErrNoShell means the backend cannot open an interactive shell here.
var ErrNoShell = errors.New("no interactive shell available")

// ErrNoLogs means the kind simply has no logs to show — a Secret, a
// Namespace, a CRD. It is not a failure: the UI shows describe instead.
// Anything else returned from a logs call is a real error worth reporting.
var ErrNoLogs = errors.New("this kind has no logs")

// CountUnknown is RowCount's answer for a kind whose data isn't loaded yet.
// A backend that watches lazily (internal/k8s) only knows counts for kinds
// the user has actually opened; the sidebar renders no badge rather than a
// misleading "0". Backends with everything in hand (internal/mock) never
// return it.
const CountUnknown = -1

// Action ids, shared between a Kind's Allowed list and Source method
// dispatch in the UI.
const (
	ADescribe = "describe"
	AYAML     = "yaml"
	ALogs     = "logs"
	AShell    = "shell"
	APortFwd  = "portfwd"
	ARestart  = "restart"
	AEdit     = "edit"
	AScale    = "scale"
	ATop      = "top"
	ACordon   = "cordon"
	ADrain    = "drain"
	ADelete   = "delete"
)

// Kind is one entry of the Resources pane: a resource kind plus which
// columns and actions apply to it.
type Kind struct {
	Key        string
	Name       string
	Short      string
	Group      string
	Namespaced bool
	Cols       []string
	Allowed    []string

	// Meta names cells each row carries past len(Cols): values the UI needs
	// but does not draw. Grouping reads them (T42).
	//
	// They are not columns. A group key has to be a value already sitting in
	// the row, because deriving one per row at render time is exactly the
	// per-row work the render path forbids — but making OWNER a real column
	// would put a cell nobody asked for on every pod table, and hiding it
	// again needs a visibility mechanism that does not exist yet. Appended
	// here instead, indexed len(Cols)+i, and ignored by every renderer
	// because they all walk Cols.
	Meta []string
}

// MetaIndex is where the meta value named key sits in a row, or -1.
func (k Kind) MetaIndex(key string) int {
	for i, m := range k.Meta {
		if m == key {
			return len(k.Cols) + i
		}
	}
	return -1
}

func (k Kind) Can(id string) bool {
	return slices.Contains(k.Allowed, id)
}

// ClusterInfo is the header's identity line.
type ClusterInfo struct {
	Context    string
	Cluster    string
	User       string
	Groups     string
	Kubeconfig string
	Server     string
	Version    string
}

// NodeInfo is one row of the header's cluster-total gauges.
type NodeInfo struct {
	Name, Status, Role, Ver string
	CPU, Mem                int // percent of allocatable, as `kubectl top node`
	// Absolute readings behind the percentages: metrics-server usage and the
	// node's allocatable, so the header can total nodes of different sizes.
	CPUMilli, CPUAllocMilli int64
	MemBytes, MemAllocBytes int64
	Age                     string
}

// Source is everything the UI needs from a cluster backend. Rows/RowCount
// take ns == "" (meaning "default") or AllNamespaces or a specific namespace
// name, and ignore ns entirely for cluster-scoped kinds.
type Source interface {
	Kinds() []Kind
	Rows(kind, ns string) (cols []string, rows [][]string)
	RowCount(kind, ns string) int

	ClusterInfo() ClusterInfo
	Nodes() []NodeInfo
	DefaultNamespace() string

	Contexts() []string
	Namespaces() []string // does not include AllNamespaces
	SwitchContext(name string) (Source, error)

	Describe(kind, ns, name string) (string, error)
	YAML(kind, ns, name string) (string, error)
	Logs(kind, ns, name string) (string, error)
	// LogsTail returns the last n lines. Asking for more than exist returns
	// what there is with more=false, which is how the viewer knows it has
	// reached the beginning of the log.
	LogsTail(kind, ns, name string, n int) (lines []string, more bool, err error)
	// LogsFollow streams new log lines as they arrive (nil channel, nil
	// error means "not supported here" — the UI falls back to Logs).
	LogsFollow(kind, ns, name string) (lines <-chan string, stop func(), err error)
	TopPod(ns, name string) (string, error)
	TopNode(name string) (string, error)

	Delete(kind, ns, name string) error
	Restart(kind, ns, name string) error
	Scale(kind, ns, name string, replicas int) (int, error)
	Cordon(name string, disabled bool) error
	Drain(name string) error
	Apply(kind, ns, name, yaml string) error

	// Shell returns a process the UI can hand the terminal to via
	// tea.ExecProcess (nil, nil if the backend has no real shell — the UI
	// then falls back to a toast).
	Shell(kind, ns, name string) (tea.ExecCommand, error)
	// ShellSession opens an interactive shell the UI can render *inside* a
	// panel rather than by handing over the whole terminal. Returns
	// ErrNoShell when the backend can't provide one.
	ShellSession(kind, ns, name string, cols, rows int) (ShellSession, error)
	// PortForward starts forwarding in the background and returns the local
	// address plus a func to stop it.
	PortForward(kind, ns, name string) (localAddr string, stop func(), err error)

	Close()
}

// NaturalLess compares case-insensitively and treats digit runs as numbers,
// so pod-2 sorts before pod-10 the way people expect.
//
// It lives here because ordering is part of what both backends and the UI
// promise: a list that reshuffles between frames makes arrow keys jump
// around, so anything the user scrolls through must have a stable order.
func NaturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(la) && j < len(lb) {
		ca, cb := la[i], lb[j]
		if isDigit(ca) && isDigit(cb) {
			si, sj := i, j
			for i < len(la) && isDigit(la[i]) {
				i++
			}
			for j < len(lb) && isDigit(lb[j]) {
				j++
			}
			na := strings.TrimLeft(la[si:i], "0")
			nb := strings.TrimLeft(lb[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(la)-i < len(lb)-j
}

// SortNames orders a list of object names for display.
func SortNames(names []string) {
	sort.SliceStable(names, func(i, j int) bool { return NaturalLess(names[i], names[j]) })
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// ---------------------------------------------------------------------------
// T29 — lens severity.
// ---------------------------------------------------------------------------

// CellLevels reports the severity bucket of one already-rendered table cell.
// It is a pure lookup over the pack's severity table — no I/O — so the render
// path may call it per visible cell. A backend that does not implement it
// simply keeps today's colouring.
//
// The cell is addressed by COLUMN NAME rather than index on purpose: ns=all
// prepends a NAMESPACE column and the table filters rows, so anything keyed by
// index drifts.
type CellLevels interface {
	// CellLevel returns "ok", "warn", "error", "unknown", or "" when this
	// column declares no severity.
	CellLevel(kind, column, value string) string
}

// ---------------------------------------------------------------------------
// T30 — lens actions.
// ---------------------------------------------------------------------------

// LensActionSpec is one declarative verb as the UI needs it: enough to draw a
// button, open the right confirm modal, and fire it by id.
//
// There is no keybinding. A lens action declares none, and the twelve builtin
// keys are taken — so lens verbs are clicked or chosen from the pane.
type LensActionSpec struct {
	ID    string
	Label string
	// Confirm is "", "true", or "typed".
	Confirm string
	// Kubectl is the equivalent command, shown in the confirm modal so the
	// operator can check the claim before agreeing to it.
	Kubectl string
	// Notice is pack-supplied text the modal shows verbatim — the ArgoCD
	// RBAC-bypass disclosure arrives this way rather than as a constant.
	Notice string
	// AckPath is non-empty when the controller publishes an acknowledgement
	// the UI can watch for.
	AckPath string
	// Disabled marks an action that cannot run right now; DisabledWhy says
	// so in the pane, instead of letting the user fire something that would
	// silently target the wrong object.
	Disabled    bool
	DisabledWhy string
	// Params are the values to collect before running, with their suggestions
	// ALREADY resolved against the cluster. The UI never evaluates a JSONPath
	// or touches a lister; it draws what it is given.
	Params []LensParamSpec
	// ConfirmValue is the template for the word a typed confirmation must
	// match, or "" to fall back to the object's own name.
	ConfirmValue string
}

// LensOption is one suggestion for a parameter. Note is the reason to pick it
// — "primary", "fenced", "completed 4h ago" — which is what the operator is
// actually choosing between; the value alone is often just a pod suffix.
type LensOption struct {
	Value string
	Note  string
}

// LensParamSpec is one parameter as the UI needs it to draw a field.
type LensParamSpec struct {
	Name    string
	Label   string
	Type    string
	Default string
	Options []LensOption
	// OptionsNote explains an EMPTY option list. "Not loaded" and "there are
	// none" are different answers, and only one of them is fixed by opening
	// that kind — a form that renders both as a blank list tells the operator
	// nothing about which they are looking at.
	OptionsNote string
	AllowFree   bool
	Required    bool
}

// LensVerbs runs a lens pack's declarative actions. A backend that does not
// implement it simply shows no lens buttons.
type LensVerbs interface {
	LensActions(kind, ns, name string) []LensActionSpec
	// LensAction runs the verb and returns the value the controller is
	// expected to echo back, or "" when the action declares no
	// acknowledgement. The caller holds it and hands it to LensAck.
	//
	// params carries the form's answers. A nil map is the normal case for an
	// action that asks nothing.
	LensAction(kind, ns, name, id string, params map[string]string) (ack string, err error)
	// LensPreview renders the equivalent kubectl command for the parameters
	// currently in the form.
	//
	// It is separate from the Kubectl string on LensActionSpec because that
	// one is computed once per selection, while this changes on every
	// keystroke — and a preview that lags the form describes a mutation other
	// than the one about to happen.
	LensPreview(kind, ns, name, id string, params map[string]string) string
	// LensAck reports whether the controller has acknowledged the write.
	//
	// want is what LensAction returned. Comparing against it is the whole
	// point: a controller that handled some EARLIER request already left a
	// non-empty value in the ack field, so testing mere presence would clear
	// the spinner before this request was ever seen.
	LensAck(kind, ns, name, id, want string) (ok bool, err error)
}

// ---------------------------------------------------------------------------
// T36 — lens relationships.
// ---------------------------------------------------------------------------

// Ref is one end of a navigable relationship.
//
// Loaded is false when the target kind has no running informer — the panel
// says so rather than opening a watch behind the user's back — or when no
// declared kind serves that GVR at all.
type Ref struct {
	// Kind is a kind key, or the raw GVR string when nothing serves it.
	Kind      string
	Namespace string
	Name      string
	// Rel is the edge's via, for the panel's label.
	Rel    string
	Loaded bool
}

// LensProblems reports a lens pack that failed to load or was rejected.
//
// It exists because the alternative is silence: a pack with a typo simply
// never appears, and "your YAML is broken" then looks exactly like "those
// CRDs are not installed on this cluster" — which is a normal, intended,
// silent outcome. Only one of the two is worth telling the user about.
type LensProblems interface {
	LensErr() error
}

// Related resolves one object's declared edges, in both directions, one hop at
// a time.
type Related interface {
	Related(kind, ns, name string) ([]Ref, error)
}
