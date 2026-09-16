package mock

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/lens"
)

// The demo's lens kinds appear only on the -prod context.
//
// That is the honest shape of the feature: lens packs are discovery-gated,
// so a cluster without ArgoCD installed does not show ArgoCD. A demo where
// every context had every operator would teach the wrong thing about what
// the real backend does.
const lensDemoContext = domain.DemoContext + "-prod"

// demoLensRows is one headline kind per tool rather than all eighteen.
// Enough to show severity colour, the verbs pane and a relationship; more
// would be filler that nobody reads.
//
// Values are chosen to exercise the grading: a Degraded app whose SYNC says
// Synced is the case that proves severity takes the WORST column, not the
// first one.
//
// Rows are written worst-first, because that is the order the real backend
// SORTS them into. The demo does not sort — so a fixture in any other order
// would put a healthy row above a failing one and teach the opposite of
// what the feature does.
var demoLensRows = map[string][][]string{
	"argocd-apps": {
		{"payments-web", "OutOfSync", "Degraded", "Failed", "7b2e4d8", "platform", "12d"},
		{"billing-cron", "Unknown", "Missing", "", "", "finance", "31d"},
		{"search-index", "Synced", "Progressing", "Running", "c81a05f", "data", "4d"},
		{"checkout-api", "Synced", "Healthy", "", "a3f91c2", "platform", "18d"},
	},
	"kargo-stages": {
		{"staging", "freight/4a7e08", "Unhealthy", "Failed", "22d"},
		{"dev", "freight/c10d3e", "Healthy", "Running", "22d"},
		{"prod", "freight/9f2c1b", "Healthy", "Succeeded", "22d"},
	},
	// AppProjects exist here only so the ArgoCD tree has somewhere to go.
	// Three rows, matching the three projects the Application fixtures name —
	// a project no Application references would make the tree look like it
	// had missed an edge.
	"argocd-projects": {
		{"platform", "prod", "deny-friday", "31d"},
		{"finance", "finance", "", "31d"},
		{"data", "data", "", "31d"},
	},
	// Three clusters, each carrying one of the failures the new columns exist
	// to surface — a failover in progress, a broken WAL archive, and a
	// three-instance cluster sitting on a single node.
	//
	// The WAL one is the case worth staring at: orders-db reports "Cluster in
	// healthy state" with every instance ready, and its archive is dead. That
	// is exactly how it looks in production, and exactly why the column is
	// there.
	"cnpg-clusters": {
		{"orders-db", "3", "3", "orders-db-1", "Cluster in healthy state", "False", "3", "4", "63d"},
		{"reporting-db", "2", "3", "reporting-db-2", "Failing over", "True", "3", "9", "21d"},
		{"billing-db", "3", "3", "billing-db-1", "Cluster in healthy state", "True", "1", "2", "12d"},
	},
	"cnpg-backups": {
		{"orders-db-k10s-8f21", "orders-db", "walArchivingFailing", "barmanObjectStore", "2h", "unexpected failure invoking barman-cloud-wal-archive: exit status 2"},
		{"reporting-db-daily-9c02", "reporting-db", "running", "volumeSnapshot", "", ""},
		{"orders-db-daily-7b31", "orders-db", "completed", "barmanObjectStore", "26h", ""},
		{"billing-db-daily-2e40", "billing-db", "completed", "barmanObjectStore", "4h", ""},
	},
	"cnpg-scheduledbackups": {
		{"reporting-db-nightly", "reporting-db", "0 0 2 * * *", "true", "31h", "cannot schedule while the cluster is failing over"},
		{"orders-db-nightly", "orders-db", "0 0 1 * * *", "false", "26h", ""},
		{"billing-db-nightly", "billing-db", "0 0 3 * * *", "false", "4h", ""},
	},
	"cnpg-poolers": {
		{"orders-rw", "orders-db", "rw", "2", "failed", "63d"},
		{"billing-ro", "billing-db", "ro", "3", "paused", "12d"},
		{"orders-ro", "orders-db", "ro", "2", "active", "63d"},
	},
	"cnpg-databases": {
		{"orders-app", "orders-db", "orders", "app", "false", `pq: permission denied to create database`, "63d"},
		{"billing-app", "billing-db", "billing", "app", "true", "", "12d"},
	},
	"cnpg-publications": {
		{"orders-outbox", "orders-db", "orders", "true", "", "40d"},
	},
	"cnpg-subscriptions": {
		{"billing-from-orders", "billing-db", "billing", "orders-outbox", "false", `could not connect to the publisher: timeout expired`, "40d"},
	},
	"cnpg-imagecatalogs": {
		{"postgres-supported", "17 18", "3", "90d"},
	},
	"lh-volumes": {
		{"pvc-b73e19da", "attached", "degraded", "200Gi", "ip-10-0-3-17", "search-index", "search-0", "v1", "12d"},
		{"pvc-04c8fe62", "detached", "unknown", "10Gi", "", "legacy-dump", "", "v1", "180d"},
		{"pvc-8f21a0c4", "attached", "healthy", "50Gi", "ip-10-0-2-88", "orders-data", "orders-db-1", "v1", "63d"},
	},
	// PAUSED true sits on the `paused` row on purpose: it is the state the
	// pack's own pause verb produces, so the table and the verbs pane agree.
	"vm-agents": {
		{"vmagent-edge", "failed", "1", "1", "false", "9d"},
		{"vmagent-shared", "expanding", "4", "8", "false", "26d"},
		{"vmagent-legacy", "paused", "1", "1", "true", "88d"},
		{"vmagent-prod", "operational", "2", "4", "false", "26d"},
	},
	"traefik-routes": {
		{"checkout", "websecure", "Host(`shop.example.com`)", "checkout-api", "rate-limit", "shop-tls", "18d"},
		{"internal-admin", "web", "Host(`admin.internal`)", "admin-ui", "", "", "90d"},
	},
}

// lensResourceDefs turns the shipped packs into demo tables.
//
// Columns and actions come from the REAL pack YAML, not from a second copy
// written here. A pack that gains a column gains it in the demo too, and a
// screenshot cannot drift from what the binary shows against a real cluster.
func lensResourceDefs() []resourceDef {
	var out []resourceDef
	for _, p := range lensPacks() {
		for _, k := range p.Kinds {
			rows, ok := demoLensRows[k.Key]
			if !ok {
				continue
			}
			cols := make([]string, len(k.Columns))
			for i, c := range k.Columns {
				cols[i] = c.Header
			}
			out = append(out, resourceDef{
				Kind: domain.Kind{
					Key: k.Key, Name: k.Name, Short: k.Short, Group: k.Group,
					Namespaced: k.Namespaced,
					Cols:       cols,
					Allowed:    append([]string{domain.ADescribe, domain.AYAML}, k.Actions...),
				},
				Rows: cloneRows(rows),
			})
		}
	}
	return out
}

// lensPacks is loaded once: parsing five embedded YAML files per keystroke
// would be the one slow thing in an otherwise in-memory backend.
var (
	lensPacksOnce sync.Once
	lensPacksVal  []lens.Pack
)

func lensPacks() []lens.Pack {
	lensPacksOnce.Do(func() { lensPacksVal, _ = lens.Builtins() })
	return lensPacksVal
}

// lensKindOf finds the pack and kind that own a key.
func lensKindOf(key string) (lens.Pack, lens.Kind, bool) {
	for _, p := range lensPacks() {
		for _, k := range p.Kinds {
			if k.Key == key {
				return p, k, true
			}
		}
	}
	return lens.Pack{}, lens.Kind{}, false
}

// CellLevel grades a demo cell using the pack's own severity tables, so the
// colours in a screenshot are the colours a real cluster produces.
func (s *Source) CellLevel(kind, column, value string) string {
	if value == "" {
		return ""
	}
	p, k, ok := lensKindOf(kind)
	if !ok {
		return ""
	}
	for _, c := range k.Columns {
		if c.Header != column {
			continue
		}
		if c.Severity == "" {
			return ""
		}
		sev, ok := p.Severities[c.Severity]
		if !ok {
			return ""
		}
		return sev.Level(value).String()
	}
	return ""
}

// LensActions lists the demo's verbs, gated exactly as the real backend
// gates them — including refusing the ones that need an instance named.
func (s *Source) LensActions(kind, ns, name, selected string) []domain.LensActionSpec {
	p, k, ok := lensKindOf(kind)
	if !ok {
		return nil
	}
	v := lens.Vars{
		Name: name, Namespace: ns, Context: contexts[s.ctxIdx],
		Now: time.Now().UTC().Format(time.RFC3339), Selected: selected,
	}
	var out []domain.LensActionSpec
	for _, id := range k.Actions {
		a, ok := p.Action(id)
		if !ok {
			continue
		}
		spec := domain.LensActionSpec{
			ID: a.ID, Label: a.Label, Confirm: a.Confirm,
			Notice: a.Notice, AckPath: a.Ack, ConfirmValue: a.ConfirmValue,
			Kubectl: lens.Kubectl(a, resourceOf(k), ns, name, v),
			Params:  demoParamSpecs(a, v),
		}
		// CheckReady, not Check: an unfilled parameter opens the form, it does
		// not disable the button.
		if err := a.CheckReady(v); err != nil {
			spec.Disabled, spec.DisabledWhy = true, err.Error()
			spec.NeedsSelection = errors.Is(err, lens.ErrSelectedRequired)
		}
		out = append(out, spec)
	}
	return out
}

// demoParamSpecs carries an action's parameters into the demo.
//
// A live optionsFrom has nothing to read offline, so its suggestions come from
// demoLensOptions — a fixture keyed by kind and parameter. Inventing them from
// the pack would make the demo claim a cluster shape it cannot show, and
// leaving them empty would make the form look broken in every screenshot.
func demoParamSpecs(a lens.Action, v lens.Vars) []domain.LensParamSpec {
	if len(a.Params) == 0 {
		return nil
	}
	// Defaults are templates and are rendered here, exactly as the real
	// backend renders them — otherwise the demo shows "{{.Name}}-restore" in
	// the field, and every screenshot taken from it shows it too.
	filled := a.Fill(v).Params
	out := make([]domain.LensParamSpec, 0, len(a.Params))
	for _, p := range a.Params {
		spec := domain.LensParamSpec{
			Name: p.Name, Label: p.Label, Type: p.Type, Default: filled[p.Name],
			AllowFree: p.AllowFree, Required: p.Required,
		}
		if spec.Label == "" {
			spec.Label = p.Name
		}
		for _, o := range p.Options {
			spec.Options = append(spec.Options, domain.LensOption{Value: o.Value, Note: o.Note})
		}
		if p.OptionsFrom != "" {
			spec.Options = append(spec.Options, demoLensOptions(p.OptionsFrom, v.Name)...)
		}
		out = append(out, spec)
	}
	return out
}

// demoLensOptions fakes one live option source.
//
// Instance names are derived from the row's own name because that is how CNPG
// names them — "<cluster>-1", "<cluster>-2" — so the demo teaches the real
// convention rather than a set of invented strings.
func demoLensOptions(source, name string) []domain.LensOption {
	switch source {
	case ".status.instanceNames":
		return []domain.LensOption{
			{Value: name + "-1", Note: "primary"},
			{Value: name + "-2", Note: "replica"},
			{Value: name + "-3", Note: "replica"},
		}
	}
	return nil
}

// resourceOf is the plural the kubectl line needs, taken off the declared
// GVR rather than guessed from the kind's name.
func resourceOf(k lens.Kind) string {
	parts := strings.Split(k.GVR, "/")
	return parts[len(parts)-1]
}

// LensAction pretends to write, and hands back a token so the demo shows
// the same acknowledgement wait a real controller produces.
func (s *Source) LensAction(kind, ns, name, id, selected string, params map[string]string) (string, error) {
	p, _, ok := lensKindOf(kind)
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}
	a, ok := p.Action(id)
	if !ok {
		return "", fmt.Errorf("lens %q has no action %q", p.Name, id)
	}
	// The demo refuses what the real backend refuses. A form that submits
	// happily here and is rejected against a cluster teaches the wrong thing
	// about the gate.
	v := a.Fill(lens.Vars{
		Name: name, Namespace: ns, Context: contexts[s.ctxIdx],
		Now: time.Now().UTC().Format(time.RFC3339), Selected: selected, Params: params,
	})
	if err := a.Check(v); err != nil {
		return "", err
	}
	if a.Ack == "" {
		return "", nil
	}
	tok := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	if s.lensAcks == nil {
		s.lensAcks = map[string]time.Time{}
	}
	// A real controller takes a moment. Answering instantly would make the
	// spinner unreachable, which is the one thing the demo is here to show.
	s.lensAcks[tok] = time.Now().Add(2500 * time.Millisecond)
	s.mu.Unlock()
	return tok, nil
}

// LensPreview renders the command for the parameters currently in the form.
func (s *Source) LensPreview(kind, ns, name, id, selected string, params map[string]string) string {
	p, k, ok := lensKindOf(kind)
	if !ok {
		return ""
	}
	a, ok := p.Action(id)
	if !ok {
		return ""
	}
	return lens.Kubectl(a, resourceOf(k), ns, name, lens.Vars{
		Name: name, Namespace: ns, Context: contexts[s.ctxIdx],
		Now: time.Now().UTC().Format(time.RFC3339), Selected: selected, Params: params,
	})
}

// LensAck reports the pretend controller as done once its delay has passed.
func (s *Source) LensAck(kind, ns, name, id, want string) (bool, error) {
	if want == "" {
		return true, nil
	}
	s.mu.RLock()
	due, ok := s.lensAcks[want]
	s.mu.RUnlock()
	if !ok {
		return true, nil
	}
	return !time.Now().Before(due), nil
}

// Related resolves the demo's relationships from the packs' own edges.
//
// The demo models rows, not object bodies, so it reports which kinds this
// one is connected to rather than inventing neighbours that do not exist.
// "not loaded" is the same wording the real backend uses for a kind nobody
// has opened, which is exactly what these are.
func (s *Source) Related(kind, ns, name string) ([]domain.Ref, error) {
	if refs, ok := demoRelated[kind+"/"+name]; ok {
		// Copied, because the caller sorts what it gets and a sort in place
		// would reorder the fixture for every later walk — which is exactly
		// the kind of run-to-run drift the tree's own sort exists to stop.
		out := append([]domain.Ref(nil), refs...)
		for i := range out {
			// The real backend reports the namespace each neighbour was
			// actually FOUND in, and a caller that walks several hops uses
			// that to tell two same-named objects apart. A demo that left it
			// empty would make every ref look cluster-scoped, so the walk
			// would fail to recognise its own starting object and draw it
			// again as its own descendant.
			if out[i].Loaded {
				out[i].Namespace = ns
			}
		}
		return out, nil
	}
	_, k, ok := lensKindOf(kind)
	if !ok {
		return nil, nil
	}
	out, in := lens.EdgesFor(lensPacks(), k.GVR)
	var refs []domain.Ref
	seen := map[string]bool{}
	for _, e := range append(append([]lens.Edge(nil), out...), in...) {
		far := e.To
		if far == k.GVR {
			far = e.From
		}
		key := demoKindForGVR(far)
		if seen[key+e.Via] {
			continue
		}
		seen[key+e.Via] = true
		refs = append(refs, domain.Ref{Kind: key, Rel: e.Via, Loaded: false})
	}
	return refs, nil
}

// demoRelated is the handful of relationships the demo can name OBJECTS for
// rather than only kinds.
//
// It exists for the tree. A tree of "not loaded" lines demonstrates nothing,
// and the demo is where the feature gets its screenshot — so the two shapes
// worth showing are modelled properly: a Kargo promotion pipeline
// (warehouse → dev → staging → prod, with the warehouse left unloaded
// because the demo has no Warehouse table) and an ArgoCD AppProject fanning
// out to the Applications it governs.
//
// Both directions are listed explicitly. The real backend derives them from
// one declaration by scanning the far side, which the demo cannot do because
// it models rows rather than object bodies; writing both halves here keeps
// what the demo shows identical to what a cluster shows.
var demoRelated = map[string][]domain.Ref{
	"kargo-stages/dev": {
		{Kind: "kargo-warehouses", Rel: lens.ViaField, Loaded: false},
		{Kind: "kargo-stages", Name: "staging", Rel: lens.ViaField, Loaded: true},
	},
	"kargo-stages/staging": {
		{Kind: "kargo-stages", Name: "dev", Rel: lens.ViaField, Loaded: true},
		{Kind: "kargo-stages", Name: "prod", Rel: lens.ViaField, Loaded: true},
	},
	"kargo-stages/prod": {
		{Kind: "kargo-stages", Name: "staging", Rel: lens.ViaField, Loaded: true},
	},
	"argocd-apps/payments-web": {
		{Kind: "argocd-projects", Name: "platform", Rel: lens.ViaField, Loaded: true},
		{Kind: "pods", Rel: lens.ViaLabel, Loaded: false},
	},
	"argocd-apps/checkout-api": {
		{Kind: "argocd-projects", Name: "platform", Rel: lens.ViaField, Loaded: true},
	},
	"argocd-apps/billing-cron": {
		{Kind: "argocd-projects", Name: "finance", Rel: lens.ViaField, Loaded: true},
	},
	"argocd-apps/search-index": {
		{Kind: "argocd-projects", Name: "data", Rel: lens.ViaField, Loaded: true},
	},
	"argocd-projects/platform": {
		{Kind: "argocd-apps", Name: "payments-web", Rel: lens.ViaField, Loaded: true},
		{Kind: "argocd-apps", Name: "checkout-api", Rel: lens.ViaField, Loaded: true},
	},
	"argocd-projects/finance": {
		{Kind: "argocd-apps", Name: "billing-cron", Rel: lens.ViaField, Loaded: true},
	},
	"argocd-projects/data": {
		{Kind: "argocd-apps", Name: "search-index", Rel: lens.ViaField, Loaded: true},
	},
}

// demoKindForGVR names an edge endpoint as a kind key where one exists, and
// leaves it as a GVR where none does.
func demoKindForGVR(gvr string) string {
	for _, p := range lensPacks() {
		for _, k := range p.Kinds {
			if k.GVR == gvr {
				return k.Key
			}
		}
	}
	return gvr
}
