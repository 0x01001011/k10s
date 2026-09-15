package k8s

import (
	"sync"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/lens"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// gvrString is the "group/version/resource" form the lens schema uses, with
// the group omitted for core types.
func gvrString(gvr schema.GroupVersionResource) string {
	if gvr.Group == "" {
		return gvr.Version + "/" + gvr.Resource
	}
	return gvr.Group + "/" + gvr.Version + "/" + gvr.Resource
}

var (
	builtinGVROnce sync.Once
	builtinGVRs    map[string]string
)

// builtinGVRIndex maps a builtin kind's GVR string back to its key.
//
// This is the piece without which half the shipped edges cannot resolve: they
// point at TYPED kinds — v1/pods, v1/persistentvolumeclaims,
// apps/v1/deployments, v1/services, v1/secrets — and gvrFor only goes
// key→GVR.
func (s *Store) builtinGVRIndex() map[string]string {
	builtinGVROnce.Do(func() {
		builtinGVRs = map[string]string{}
		for _, k := range builtinKinds {
			gvr, _, err := s.gvrFor(k.Key)
			if err != nil {
				continue
			}
			builtinGVRs[gvrString(gvr)] = k.Key
		}
	})
	return builtinGVRs
}

// kindKeyForGVR resolves an edge endpoint to a kind key. A GVR that no kind
// serves is a first-class outcome, not a failure: Longhorn's replicas and
// engines are edge endpoints that no pack declares as a browsable kind.
func (s *Store) kindKeyForGVR(gvr string) (string, bool) {
	if key, ok := s.builtinGVRIndex()[gvr]; ok {
		return key, true
	}
	// byGVR, not a scan of byKey: map iteration order is randomized, so two
	// kinds over the same GVR would resolve to a different one per run and
	// the panel would point somewhere else each time. byGVR keeps the first
	// pack in registry order, matching buildLensReg's claim order.
	if r := s.lensSnapshot(); r != nil {
		g, v, res, err := lens.ParseGVR(gvr)
		if err == nil {
			if lk, ok := r.byGVR[schema.GroupVersionResource{Group: g, Version: v, Resource: res}]; ok {
				return lk.kind.Key, true
			}
		}
	}
	return "", false
}

// cachedObjects returns the objects of key from an ALREADY-STARTED informer.
//
// ok=false means "not loaded" and is never a reason to start one: resolving a
// relationship must not open a watch the user did not ask for. It works
// uniformly for typed and dynamic informers because both are reached through
// informerLocked.
func (s *Store) cachedObjects(key, ns string) ([]any, bool) {
	s.infMu.Lock()
	defer s.infMu.Unlock()
	desired := s.desiredInformerScope(key, []string{ns})
	scope := s.accessScopeLocked(key, desired)
	if !s.started[informerKey{kind: key, namespace: scope}] {
		return nil, false
	}
	inf := s.informerLocked(key, scope)
	if inf == nil {
		return nil, false
	}
	return inf.GetStore().List(), true
}

// objAsMap gives lens.Values the raw map it needs for `via: field`.
//
// Unstructured objects pass through untouched. A typed object is converted,
// which costs an allocation — but in the shipped packs every field path is on
// the lens (unstructured) side, so that branch is a correctness fallback for
// user packs rather than a hot path. Related runs once per keypress.
func objAsMap(o any) map[string]any {
	if u, ok := o.(*unstructured.Unstructured); ok {
		return u.Object
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
	if err != nil {
		return nil
	}
	return m
}

// Related resolves one object's declared edges, in both directions, ONE HOP at
// a time.
//
// One hop is what makes cycles a non-problem: a TraefikService referencing
// itself cannot hang a renderer that never recurses. Each neighbour is a
// navigation target rather than an expandable node, so depth limiting is
// achieved by construction instead of by a counter.
func (s *Store) Related(kind, ns, name string) ([]domain.Ref, error) {
	gvr, _, err := s.gvrFor(kind)
	if err != nil {
		return nil, err
	}
	r := s.lensSnapshot()
	if r == nil {
		return nil, nil
	}
	me := gvrString(gvr)
	out, in := lens.EdgesFor(r.packs, me)
	if len(out) == 0 && len(in) == 0 {
		return nil, nil
	}

	var refs []domain.Ref
	seen := map[string]bool{}
	add := func(ref domain.Ref) {
		// Exclude the object from its own result.
		if ref.Kind == kind && ref.Name == name && ref.Namespace == ns {
			return
		}
		k := ref.Kind + "\x00" + ref.Namespace + "\x00" + ref.Name + "\x00" + ref.Rel
		if seen[k] {
			return
		}
		seen[k] = true
		refs = append(refs, ref)
	}

	// Outgoing: read MY object and follow what it points at.
	if len(out) > 0 {
		if mine, myNS := s.cachedObjectByName(kind, ns, name); mine != nil {
			for _, e := range out {
				key, known := s.kindKeyForGVR(e.To)
				target := key
				if !known {
					target = e.To
				}
				for _, n := range lens.Values(e, mine) {
					// The neighbour's namespace is LOOKED UP, never assumed
					// from the source. ArgoCD is the case that proves it: an
					// Application lives in the Argo install namespace while
					// the workloads it manages live anywhere, so copying the
					// pod's namespace onto the Application names an object
					// that does not exist.
					refNS, found := "", false
					if known {
						refNS, found = s.cachedNamespaceOf(key, myNS, n)
					}
					add(domain.Ref{Kind: target, Namespace: refNS, Name: n, Rel: e.Via, Loaded: found})
				}
			}
		}
	}

	// Incoming: scan the far side for objects that name ME.
	for _, e := range in {
		key, known := s.kindKeyForGVR(e.From)
		if !known {
			// No declared kind serves this GVR. Report it rather than
			// dropping it: "no view for x" is a different problem from
			// "not loaded", and only one of them is fixable by opening it.
			add(domain.Ref{Kind: e.From, Rel: e.Via, Loaded: false})
			continue
		}
		objs, ok := s.cachedObjects(key, ns)
		if !ok {
			add(domain.Ref{Kind: key, Rel: e.Via, Loaded: false})
			continue
		}
		for _, o := range objs {
			m := objAsMap(o)
			if m == nil {
				continue
			}
			if !contains(lens.Values(e, m), name) {
				continue
			}
			acc, err := meta.Accessor(o)
			if err != nil {
				continue
			}
			// An ownerRef cannot cross a namespace — Kubernetes forbids it —
			// so a name match in another namespace is a different object,
			// and the cache is often cluster-wide (accessScopeLocked reuses
			// a started all-namespaces informer). Without this a Cluster
			// called "pg" in namespace a collects the pods of the unrelated
			// "pg" in namespace b.
			//
			// Every other edge kind matches a label, annotation or field
			// VALUE, which carries no namespace and is routinely used across
			// them: an ArgoCD Application in the argocd namespace labels
			// workloads in every other namespace. Those are kept, and each
			// Ref carries the namespace it was actually found in, so two
			// same-named neighbours stay distinguishable instead of one
			// being silently attributed to the other's namespace.
			if e.Via == lens.ViaOwnerRef && !sameNamespace(acc.GetNamespace(), ns) {
				continue
			}
			add(domain.Ref{Kind: key, Namespace: acc.GetNamespace(), Name: acc.GetName(), Rel: e.Via, Loaded: true})
		}
	}
	return refs, nil
}

// cachedNamespaceOf finds where a named object of kind actually lives,
// reading only an already-started informer.
//
// hintNS is where to ask — the source object's namespace — because that is
// the scope most likely to be open; accessScopeLocked promotes it to the
// cluster-wide cache when one is running, which is how an Application in the
// argocd namespace is found from a pod in prod. Not found is reported as
// such rather than guessed at, so the panel says "not loaded" instead of
// naming an object that is not there.
func (s *Store) cachedNamespaceOf(kind, hintNS, name string) (string, bool) {
	objs, ok := s.cachedObjects(kind, hintNS)
	if !ok {
		return "", false
	}
	for _, o := range objs {
		acc, err := meta.Accessor(o)
		if err != nil || acc.GetName() != name {
			continue
		}
		return acc.GetNamespace(), true
	}
	return "", false
}

// cachedObjectByName reads one object of any kind from an already-started
// informer, as a raw map, and reports the namespace it actually lives in.
// Never starts a watch.
func (s *Store) cachedObjectByName(kind, ns, name string) (map[string]any, string) {
	objs, ok := s.cachedObjects(kind, ns)
	if !ok {
		return nil, ""
	}
	for _, o := range objs {
		acc, err := meta.Accessor(o)
		if err != nil || acc.GetName() != name {
			continue
		}
		if !sameNamespace(acc.GetNamespace(), ns) {
			continue
		}
		return objAsMap(o), acc.GetNamespace()
	}
	return nil, ""
}

// sameNamespace reports whether an object's namespace matches the view's.
// A cluster-scoped object (empty namespace) matches any view, and the
// AllNamespaces sentinel matches everything.
func sameNamespace(objNS, viewNS string) bool {
	if objNS == "" || viewNS == domain.AllNamespaces {
		return true
	}
	eff := viewNS
	if eff == "" {
		eff = "default"
	}
	return objNS == eff
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
