package ui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
)

// levelFor returns the severity lookup for one kind, or nil when the backend
// grades nothing.
//
// The nil return is the point: callers branch once per frame instead of
// paying an interface assertion on every cell of every row.
func (m *Model) levelFor(kind string) func(column, value string) string {
	cl, ok := m.src.(domain.CellLevels)
	if !ok {
		return nil
	}
	return func(column, value string) string {
		return cl.CellLevel(kind, column, value)
	}
}

// lensAckState is one outstanding wait for a controller to acknowledge a
// write. Kargo echoes the refresh token into .status.lastHandledRefresh;
// until it does, the request is in flight and the pane says so.
type lensAckState struct {
	kind, ns, name string
	id, label      string
	want           string
	started        time.Time
	frame          int
	// seq identifies THIS request. The action id cannot: it is the same
	// string for every row and every invocation, so refreshing Stage A and
	// then Stage B would let A's reply clear B's spinner and report B as
	// acknowledged when only A was.
	seq int
}

// lensAckPoll is the gap between cache reads while waiting. The cache is
// local, so this costs nothing on the wire — but a controller that is
// working takes seconds, and a tighter loop only burns frames.
const lensAckPoll = 600 * time.Millisecond

// lensAckTimeout bounds the wait. Past it the write stands but the
// acknowledgement never came, which is a different thing from a failure and
// is reported as one.
const lensAckTimeout = 45 * time.Second

var lensSpinner = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// lensActions lists the declarative verbs for the selected row, memoised by
// the row's identity.
//
// The memo is what keeps this off the render path in spirit as well as in
// fact: listing evaluates every action's preconditions against the informer
// cache, and View runs on every keystroke while the selection does not.
func (m *Model) lensActions() []domain.LensActionSpec {
	lv, ok := m.src.(domain.LensVerbs)
	if !ok {
		return nil
	}
	kind, ns, name := m.curKind().Key, m.curNamespace(), m.curName()
	if name == "" || name == "-" {
		return nil
	}
	row := kind + "\x00" + ns + "\x00" + name
	sel := m.selectedFor(row)
	key := row + "\x00" + sel
	if key == m.lensKey {
		return m.lensSpecs
	}
	m.lensKey = key
	m.lensSpecs = lv.LensActions(kind, ns, name, sel)
	return m.lensSpecs
}

// selectedFor returns the instance the operator named, but ONLY for the row
// they named it on.
//
// Carrying it further is the dangerous case: having answered "which
// instance?" with db-a-2 while fencing cluster db-a, moving to db-b and
// pressing the same key would pass Check (Selected is non-empty), skip the
// prompt entirely, and annotate db-b with an instance that belongs to db-a
// — fencing nothing, on the wrong cluster, silently. The selection is scoped
// to its row for exactly that reason.
func (m *Model) selectedFor(row string) string {
	if m.lensSelKey != row {
		return ""
	}
	return m.lensSel
}

// lensKeyFor is the digit that fires the nth lens action. Digits are used
// because every letter worth having is already an action or a plugin
// shortcut, and because "2" reads as "the second one in the list" without
// anybody having to learn a mnemonic.
func lensKeyFor(i int) string {
	if i > 8 {
		return ""
	}
	return strconv.Itoa(i + 1)
}

// fireLensKey dispatches a digit to the lens action it names. The bool
// reports whether the digit belonged to a lens action at all, so a digit
// nobody claims can fall through to whatever else wants it.
func (m *Model) fireLensKey(key string) (tea.Cmd, bool) {
	specs := m.lensActions()
	for i, sp := range specs {
		if lensKeyFor(i) != key {
			continue
		}
		return m.fireLensAction(sp), true
	}
	return nil, false
}

// fireLensAction runs one verb, stopping at whatever gate it declares:
// disabled outright, a question to answer, a confirmation, a word to type.
func (m *Model) fireLensAction(sp domain.LensActionSpec) tea.Cmd {
	kind, ns, name := m.curKind().Key, m.curNamespace(), m.curName()
	short := m.curKind().Short

	// An action that needs an instance asks for one rather than refusing.
	// This is the only reason these verbs would otherwise be unreachable:
	// a CNPG instance is "my-db-2", never the cluster's own name, so there
	// is nothing sensible to default to.
	if sp.Disabled && sp.NeedsSelection {
		row := kind + "\x00" + ns + "\x00" + name
		m.confirm = &confirmState{
			title:   sp.Label,
			ask:     "which instance?",
			message: []string{sp.Label, short + "/" + name, "namespace: " + ns},
			onOK: func(mm *Model) tea.Cmd {
				mm.lensSel, mm.lensSelKey = mm.confirmAnswer, row
				mm.lensKey = "" // the answer changes what the backend returns
				for _, s2 := range mm.lensActions() {
					if s2.ID == sp.ID {
						return mm.fireLensAction(s2)
					}
				}
				return nil
			},
		}
		return nil
	}

	// Every other disabled action still says why. The reason is the useful
	// half — "ArgoCD is already syncing" is an answer, a silent no-op is not.
	if sp.Disabled && sp.DisabledWhy != "" {
		m.toast = "✗ " + sp.DisabledWhy
		return nil
	}

	// An action that declares parameters collects them in a form, which owns
	// its own confirmation: the word to type is one of the values being
	// chosen, so it cannot be settled before they are.
	if len(sp.Params) > 0 {
		m.openLensForm(kind, ns, name, short, sp)
		return nil
	}

	run := func(mm *Model) tea.Cmd { return mm.runLensAction(kind, ns, name, short, sp, nil) }

	switch sp.Confirm {
	case "typed":
		m.confirm = &confirmState{
			title:   sp.Label,
			danger:  true,
			typed:   name,
			message: lensConfirmBody(sp, short, ns, name),
			onOK:    run,
		}
		return nil
	case "true":
		m.confirm = &confirmState{
			title:   sp.Label,
			message: lensConfirmBody(sp, short, ns, name),
			onOK:    run,
		}
		return nil
	}
	return run(m)
}

// lensConfirmBody spells out what is about to happen, ending with the
// equivalent kubectl line. Showing the command is not decoration: it is how
// an operator checks that the button does what they think, and how they
// reproduce it in a runbook afterwards.
// The label is NOT repeated here: overlayConfirm already renders it as the
// modal's title, and a 58-column box cannot spare a row to say it twice.
func lensConfirmBody(sp domain.LensActionSpec, short, ns, name string) []string {
	body := []string{short + "/" + name}
	if ns != "" {
		body = append(body, "namespace: "+ns)
	}
	if sp.Notice != "" {
		body = append(body, "")
		body = append(body, wrapNotice(sp.Notice, 52)...)
	}
	if sp.Kubectl != "" {
		body = append(body, "", sp.Kubectl)
	}
	return body
}

// wrapNotice breaks a security notice onto lines the modal can hold. The
// notices are the one part of a pack that must be read, so they are wrapped
// rather than truncated.
func wrapNotice(s string, width int) []string {
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// runLensAction performs the write, then starts waiting for the controller
// if the action declares an acknowledgement field.
func (m *Model) runLensAction(kind, ns, name, short string, sp domain.LensActionSpec, params map[string]string) tea.Cmd {
	lv, ok := m.src.(domain.LensVerbs)
	if !ok {
		return nil
	}
	sel := m.selectedFor(kind + "\x00" + ns + "\x00" + name)
	label := sp.Label + " " + short + "/" + name
	m.lensSeq++
	seq := m.lensSeq
	m.startBusy(label)
	return func() tea.Msg {
		ack, err := lv.LensAction(kind, ns, name, sp.ID, sel, params)
		return lensDoneMsg{
			kind: kind, ns: ns, name: name,
			id: sp.ID, label: label, seq: seq,
			ackPath: sp.AckPath, want: ack,
			err: err,
		}
	}
}

// lensDoneMsg lands when the write itself has returned.
type lensDoneMsg struct {
	kind, ns, name string
	id, label      string
	seq            int
	ackPath, want  string
	err            error
}

// lensAckMsg lands on each poll of the acknowledgement field.
type lensAckMsg struct {
	seq  int
	ok   bool
	err  error
	done bool // the wait timed out
}

// handleLensDone turns a completed write into either a finished toast or an
// ongoing wait.
func (m *Model) handleLensDone(msg lensDoneMsg) tea.Cmd {
	// A write the user has since superseded reports nothing: its toast would
	// overwrite the newer one, and its wait is not the wait on screen.
	if msg.seq != m.lensSeq {
		return nil
	}
	if msg.err != nil {
		m.lensAck = nil
		m.busy = false
		// Server errors are shown verbatim: a Kargo promotion refusal names
		// the permission that is missing, and paraphrasing loses it.
		m.toast = "✗ " + msg.err.Error()
		return nil
	}
	if msg.ackPath == "" {
		m.lensAck = nil
		m.busy = false
		m.toast = "✓ " + msg.label
		return nil
	}
	m.lensAck = &lensAckState{
		kind: msg.kind, ns: msg.ns, name: msg.name,
		id: msg.id, label: msg.label, seq: msg.seq,
		want: msg.want, started: time.Now(),
	}
	return m.pollLensAck()
}

// pollLensAck schedules the next cache read. The wait's sequence travels
// with it so a reply for a wait the user has since replaced is dropped
// rather than clearing the current one.
func (m *Model) pollLensAck() tea.Cmd {
	lv, ok := m.src.(domain.LensVerbs)
	if !ok {
		return nil
	}
	st := m.lensAck
	if st == nil {
		return nil
	}
	kind, ns, name, id, want, seq := st.kind, st.ns, st.name, st.id, st.want, st.seq
	timedOut := time.Since(st.started) > lensAckTimeout
	return tea.Tick(lensAckPoll, func(time.Time) tea.Msg {
		if timedOut {
			return lensAckMsg{seq: seq, done: true}
		}
		ok, err := lv.LensAck(kind, ns, name, id, want)
		return lensAckMsg{seq: seq, ok: ok, err: err}
	})
}

// handleLensAck advances or ends the wait.
func (m *Model) handleLensAck(msg lensAckMsg) tea.Cmd {
	st := m.lensAck
	if st == nil || st.seq != msg.seq {
		return nil
	}
	switch {
	case msg.err != nil:
		m.lensAck = nil
		m.busy = false
		m.toast = "✗ " + msg.err.Error()
	case msg.ok:
		m.lensAck = nil
		m.busy = false
		m.toast = "✓ " + st.label
	case msg.done:
		m.lensAck = nil
		m.busy = false
		// Not a failure: the write went through and the controller has not
		// answered yet. Saying "failed" here would be a lie that sends the
		// operator to retry a request that is already queued.
		m.toast = "⧗ " + st.label + " — sent, no acknowledgement yet"
	default:
		st.frame++
		return m.pollLensAck()
	}
	return nil
}

// showRelated lists what the selected object is connected to, one hop out,
// in the text panel that already exists for describe and YAML.
//
// One hop, because each neighbour is a place to navigate to rather than a
// tree to expand: a TraefikService that references itself cannot hang a
// renderer that never recurses.
func (m *Model) showRelated() tea.Cmd {
	rel, ok := m.src.(domain.Related)
	if !ok {
		m.toast = "✗ this backend has no relationships"
		return nil
	}
	kind, ns, name := m.curKind().Key, m.curNamespace(), m.curName()
	if name == "" || name == "-" {
		return nil
	}
	title := "related " + m.curKind().Short + "/" + name
	return m.runFetch(title, func() (string, error) {
		refs, err := rel.Related(kind, ns, name)
		if err != nil {
			return "", err
		}
		return renderRelated(refs), nil
	})
}

// renderRelated formats one hop of neighbours, grouped by relationship.
//
// A neighbour whose kind is not loaded is listed and marked, not hidden:
// "not loaded" and "does not exist" are different answers, and only one of
// them is fixed by opening that kind.
func renderRelated(refs []domain.Ref) string {
	if len(refs) == 0 {
		return "No declared relationships resolved for this object.\n\n" +
			"Relationships come from the lens pack's edges. An edge whose\n" +
			"far side has never been opened reads as not loaded, not as absent."
	}
	byRel := map[string][]domain.Ref{}
	var order []string
	for _, r := range refs {
		if _, seen := byRel[r.Rel]; !seen {
			order = append(order, r.Rel)
		}
		byRel[r.Rel] = append(byRel[r.Rel], r)
	}
	var b strings.Builder
	for _, rel := range order {
		b.WriteString("via " + rel + "\n")
		for _, r := range byRel[rel] {
			b.WriteString("  " + r.Kind)
			if r.Namespace != "" {
				b.WriteString("  " + r.Namespace + "/")
			} else {
				b.WriteString("  ")
			}
			b.WriteString(r.Name)
			if !r.Loaded {
				b.WriteString("   (not loaded — open this kind to resolve)")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// noticeLensErr reports a broken lens pack once.
//
// Once, because the error is permanent for the session — the gate runs a
// single time — and repeating it on every tick would make the toast useless
// for everything else. A pack that is merely gated off by discovery is NOT
// an error and says nothing, which is the whole point of the distinction.
func (m *Model) noticeLensErr() {
	if m.lensErrSeen {
		return
	}
	lp, ok := m.src.(domain.LensProblems)
	if !ok {
		return
	}
	err := lp.LensErr()
	if err == nil {
		return
	}
	m.lensErrSeen = true
	m.toast = "✗ lens pack: " + err.Error()
}

// reanchorRow keeps the cursor on the OBJECT it was on, not on the index.
//
// Lens tables are sorted worst-first, so any status change reorders them:
// on a busy ArgoCD install a row the operator never touched can transition
// and shift everything below it. rowIdx is purely positional, so without
// this the next keystroke — edit, delete, sync, roll back — would land on a
// different Application than the one under the highlight.
//
// It runs on the repaint tick, which is where a reorder becomes visible,
// and costs one scan of the visible table.
func (m *Model) reanchorRow() {
	if m.rowAnchor == "" || m.mode != modeTable {
		return
	}
	cols, rows := m.tableData()
	if len(rows) == 0 {
		return
	}
	// Resolving the identity columns is per-TABLE work, and so is tableData
	// itself — it re-reads the store and copies every row. Doing either
	// inside the scan made a tick that only moves a cursor cost O(rows²)
	// copies of the whole table.
	id := rowIdentity(m, cols)
	wantNS, wantName, _ := strings.Cut(m.rowAnchor, "/")
	if m.rowIdx >= 0 && m.rowIdx < len(rows) && id.is(rows[m.rowIdx], wantNS, wantName) {
		return
	}
	for i, r := range rows {
		if id.is(r, wantNS, wantName) {
			m.rowIdx = i
			m.rowMem[m.curKind().Key] = i
			return
		}
	}
	// The object is gone. The index is the only thing left to keep, and the
	// anchor is dropped so a later row of the same name is not chased.
	m.rowAnchor = ""
}

// anchorRow records the object under the cursor, so a reorder can find it
// again. Called wherever the USER moves the selection — never from a
// refresh, which is the thing being defended against.
func (m *Model) anchorRow() {
	if m.mode != modeTable {
		return
	}
	cols, rows := m.tableData()
	if m.rowIdx < 0 || m.rowIdx >= len(rows) || len(rows[m.rowIdx]) == 0 {
		m.rowAnchor = ""
		return
	}
	m.rowAnchor = rowIdentity(m, cols).name(rows[m.rowIdx])
}

// rowIdent is where a row's namespace and name live, resolved by header
// rather than by index: under :ns all a NAMESPACE column shifts everything
// right. nsIdx is -1 when the table has no namespace column, and nameIdx
// falls back to column 0 when the header is missing — same as before.
type rowIdent struct{ nameIdx, nsIdx int }

// rowIdentity resolves those columns once for the table on screen.
func rowIdentity(m *Model, cols []string) rowIdent {
	return identFor(m.curKind().Key, cols)
}

// identFor is the same resolution for a kind that is NOT the one on screen.
// The tree grades neighbours of every kind it walks into, and each of those
// carries its own header row.
func identFor(kind string, cols []string) rowIdent {
	key := "NAME"
	if kind == "events" {
		key = "OBJECT"
	}
	id := rowIdent{nameIdx: 0, nsIdx: -1}
	for i, c := range cols {
		switch c {
		case key:
			id.nameIdx = i
		case "NAMESPACE":
			id.nsIdx = i
		}
	}
	return id
}

func (id rowIdent) cells(row []string) (ns, name string) {
	name = row[0]
	if id.nameIdx < len(row) {
		name = row[id.nameIdx]
	}
	if id.nsIdx >= 0 && id.nsIdx < len(row) {
		ns = row[id.nsIdx]
	}
	return ns, name
}

// name is the stored anchor form. Built once per anchoring, never inside the
// scan — the scan compares the halves instead, so it allocates nothing.
func (id rowIdent) name(row []string) string {
	ns, name := id.cells(row)
	return ns + "/" + name
}

func (id rowIdent) is(row []string, ns, name string) bool {
	if len(row) == 0 {
		return false
	}
	rns, rname := id.cells(row)
	return rname == name && rns == ns
}

// lensAckGlyph is the spinner frame for the pane, or "" when nothing is
// pending.
func (m *Model) lensAckGlyph() string {
	if m.lensAck == nil {
		return ""
	}
	return lensSpinner[m.lensAck.frame%len(lensSpinner)]
}
