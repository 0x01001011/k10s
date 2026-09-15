package k8s

import (
	"fmt"
	"strings"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/lens"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// lensKind is one resource type contributed by a lens pack: the domain.Kind
// the UI sees, the GVR the dynamic client needs, and the pack it came from so
// severity tables and actions stay reachable.
type lensKind struct {
	kind domain.Kind
	gvr  schema.GroupVersionResource
	pack lens.Pack
	src  lens.Kind
	cols []lensCol
}

// lensReg is an immutable snapshot. It is built once, published once via
// Store.lensSnap, and never mutated — so every reader is lock-free and no
// reader can observe a half-built registry.
type lensReg struct {
	order []domain.Kind
	byKey map[string]*lensKind
	byGVR map[schema.GroupVersionResource]*lensKind
	packs []lens.Pack
}

// servedGroupVersions makes exactly ONE aggregated discovery round trip and
// returns the group/versions the API server serves.
//
// ServerGroups is used rather than ServerPreferredResources because the
// latter issues a GET per group-version — dozens on a real cluster — and the
// gate only needs to know which group/versions exist, not their resources.
//
// A nil client or a total failure yields an empty set, which gates every pack
// off. That is the safe direction: showing no lens kinds is a missing feature,
// while showing kinds whose CRDs are absent is a table of errors.
func servedGroupVersions(d discovery.DiscoveryInterface) map[string]bool {
	out := map[string]bool{}
	if d == nil {
		return out
	}
	// ServerGroups can return a partial result alongside an error on a
	// cluster with a broken aggregated API. Whatever came back is still
	// true, so it is used; only a nil result closes the gate.
	groups, _ := d.ServerGroups()
	if groups == nil {
		return out
	}
	for _, g := range groups.Groups {
		for _, v := range g.Versions {
			out[v.GroupVersion] = true
		}
	}
	return out
}

// gatePacks keeps only the packs whose every `requires` entry is served AND
// whose kinds' resources actually exist.
//
// The group/version check alone is not enough, and the case is not
// hypothetical: Argo Workflows, Argo Rollouts and Argo Events all serve
// `argoproj.io/v1alpha1` without ever installing the Application CRD. A
// cluster running Rollouts and no ArgoCD would pass the gate and get a
// sidebar full of kinds whose every LIST is a 404.
//
// resourcesIn is consulted only for packs that already passed the cheap
// check, and is memoised per group-version, so this costs a handful of GETs
// on a cluster that has these operators and none on one that does not.
func gatePacks(packs []lens.Pack, served map[string]bool, resourcesIn func(gv string) map[string]bool) []lens.Pack {
	out := make([]lens.Pack, 0, len(packs))
	for _, p := range packs {
		ok := true
		for _, gv := range p.Requires {
			if !served[gv] {
				ok = false
				break
			}
		}
		if ok && resourcesIn != nil {
			ok = kindsServed(p, resourcesIn)
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}

// kindsServed reports whether EVERY kind the pack declares has its resource
// served. All, not any: a pack is a coherent view of one operator, and half
// of it is a table of errors.
//
// A group-version whose resources cannot be listed is treated as served: a
// discovery call that fails is not evidence of absence, the pack already
// cleared the group/version gate, and closing it here would delete a working
// view over a transient API hiccup.
func kindsServed(p lens.Pack, resourcesIn func(gv string) map[string]bool) bool {
	for _, k := range p.Kinds {
		g, v, res, err := lens.ParseGVR(k.GVR)
		if err != nil {
			return false
		}
		gv := v
		if g != "" {
			gv = g + "/" + v
		}
		known := resourcesIn(gv)
		if known == nil {
			continue
		}
		if !known[res] {
			return false
		}
	}
	return true
}

// servedResourcesFunc answers "which resources does this group-version
// serve", once per group-version, from the API server.
func servedResourcesFunc(d discovery.DiscoveryInterface) func(gv string) map[string]bool {
	if d == nil {
		return nil
	}
	cache := map[string]map[string]bool{}
	return func(gv string) map[string]bool {
		if got, seen := cache[gv]; seen {
			return got
		}
		var out map[string]bool
		// An EMPTY resource list is read as "no detail", not as "no
		// resources". A real API server never serves a group-version with
		// nothing in it, so the only thing this distinction protects is the
		// honest reading of a discovery response that told us nothing —
		// which must not delete a pack that already cleared the first gate.
		if list, err := d.ServerResourcesForGroupVersion(gv); err == nil && list != nil && len(list.APIResources) > 0 {
			out = make(map[string]bool, len(list.APIResources))
			for _, r := range list.APIResources {
				out[r.Name] = true
			}
		}
		cache[gv] = out
		return out
	}
}

// builtinTaken is every key and alias a lens pack must not claim: the builtin
// kinds' keys and shorts. The "cr|" prefix encodeCRGVR uses to smuggle a
// runtime-discovered GVR through the same string channel is rejected
// separately, by prefix.
func builtinTaken() map[string]bool {
	taken := map[string]bool{}
	for _, k := range builtinKinds {
		taken[k.Key] = true
		if k.Short != "" {
			taken[k.Short] = true
		}
	}
	return taken
}

// buildLensReg compiles the gated packs into an immutable registry.
//
// A pack with ANY colliding kind is dropped WHOLE rather than partially: its
// actions and edges reference its own kinds, so keeping half of it would leave
// dangling references that fail later and further from the cause.
func buildLensReg(packs []lens.Pack, taken map[string]bool) (*lensReg, []error) {
	reg := &lensReg{
		byKey: map[string]*lensKind{},
		byGVR: map[schema.GroupVersionResource]*lensKind{},
	}
	var errs []error

	claimed := map[string]bool{}
	for k := range taken {
		claimed[k] = true
	}

	for _, p := range packs {
		if err := packCollision(p, claimed); err != nil {
			errs = append(errs, err)
			continue
		}
		compiled, err := compilePack(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Claim only once the whole pack is known good, so a rejected pack
		// leaves no reservations behind.
		for _, lk := range compiled {
			claimed[lk.kind.Key] = true
			if lk.kind.Short != "" {
				claimed[lk.kind.Short] = true
			}
			reg.order = append(reg.order, lk.kind)
			reg.byKey[lk.kind.Key] = lk
			// FIRST pack over a GVR wins, matching registry order. Two packs
			// may legitimately view the same resource through different
			// columns; letting the last one win would make which kind an
			// edge resolves to depend on load order rather than on anything
			// the user can see.
			if _, dup := reg.byGVR[lk.gvr]; !dup {
				reg.byGVR[lk.gvr] = lk
			}
		}
		reg.packs = append(reg.packs, p)
	}
	return reg, errs
}

// startLensGate runs the discovery gate once, off the connect path, publishes
// the snapshot and closes lensReady.
//
// It is the ONLY writer of lensSnap, and it publishes exactly once, so every
// reader sees either nil or a complete registry. SwitchContext builds a whole
// new Store (it never mutates the receiver), so moving to a cluster with a
// different set of operators re-runs this automatically.
func (s *Store) startLensGate() {
	defer close(s.lensReady)

	// Packs are loaded here rather than in the constructor so that every
	// existing test — all of which build a Client with a nil Discovery —
	// never reads the developer's real ~/.k10s/lenses directory.
	if s.c == nil || s.c.Discovery == nil {
		return
	}
	packs, errs := lens.Load(lens.Dir())
	for _, err := range errs {
		// A broken user pack costs that pack, not the session.
		s.lensErr(err)
	}
	gated := gatePacks(packs, servedGroupVersions(s.c.Discovery), servedResourcesFunc(s.c.Discovery))
	reg, errs := buildLensReg(gated, builtinTaken())
	for _, err := range errs {
		s.lensErr(err)
	}
	s.lensSnap.Store(reg)
}

// lensErr records a pack-level problem. Lens failures are never fatal: the
// user loses one pack's kinds, not the cluster.
func (s *Store) lensErr(err error) {
	if err == nil {
		return
	}
	s.infMu.Lock()
	s.loadErr[informerKey{kind: "lens"}] = err
	s.infMu.Unlock()
}

// LensErr reports the last lens pack problem, if any. It is surfaced the same
// way a load error is: visibly, but without blocking anything.
func (s *Store) LensErr() error {
	s.infMu.Lock()
	defer s.infMu.Unlock()
	return s.loadErr[informerKey{kind: "lens"}]
}

func (s *Store) lensSnapshot() *lensReg { return s.lensSnap.Load() }

func (s *Store) lensKindFor(key string) (*lensKind, bool) {
	r := s.lensSnap.Load()
	if r == nil {
		return nil, false
	}
	lk, ok := r.byKey[key]
	return lk, ok
}

// isLensKindLocked is lensKindFor for callers already holding infMu. The
// snapshot is atomic and immutable, so this takes no lock of its own — the
// name records the calling context, not a lock it acquires.
func (s *Store) isLensKindLocked(key string) bool {
	_, ok := s.lensKindFor(key)
	return ok
}

// findKind is the Store-scoped kind oracle: builtin first, then lens. The
// package-level findKind stays untouched for callers that genuinely mean
// "builtin only".
func (s *Store) findKind(key string) *domain.Kind {
	if k := findKind(key); k != nil {
		return k
	}
	if lk, ok := s.lensKindFor(key); ok {
		return &lk.kind
	}
	return nil
}

// informerLocked is the single kind→informer oracle. A lens key resolves to a
// dynamic informer; every other kind falls through to register(), untouched.
//
// Two invariants, both easy to break later:
//
//   - All four callers already hold s.infMu. This must not take it.
//   - This must not dial. dynamicinformer's ForResource only constructs and
//     caches the informer under the factory's own lock; Start is what launches
//     it, and only ensure calls Start.
//
// The second matters more than it looks. SyncedFor and LoadErrorFor both check
// s.started BEFORE reaching here, so a mere sidebar query never registers a
// dynamic informer — and DynamicSharedInformerFactory.Start launches EVERY
// informer registered on it, so one stray registration would silently widen
// the watch set on the next ensure.
func (s *Store) informerLocked(kind, scope string) cache.SharedIndexInformer {
	if lk, ok := s.lensKindFor(kind); ok {
		return s.dynFactoryLocked(scope).ForResource(lk.gvr).Informer()
	}
	return register(s.factoryLocked(scope), s.apiextFactory, kind)
}

// dynFactoryLocked mirrors factoryLocked for the dynamic client. Resync is 0:
// no event handlers are registered, so periodic resyncs would be pure
// overhead. Called under infMu.
func (s *Store) dynFactoryLocked(namespace string) dynamicinformer.DynamicSharedInformerFactory {
	if namespace == metav1.NamespaceAll {
		if s.dynFactory == nil {
			s.dynFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(s.c.Dynamic, 0, metav1.NamespaceAll, nil)
		}
		return s.dynFactory
	}
	if f := s.dynFactories[namespace]; f != nil {
		return f
	}
	f := dynamicinformer.NewFilteredDynamicSharedInformerFactory(s.c.Dynamic, 0, namespace, nil)
	s.dynFactories[namespace] = f
	return f
}

// lensLister opens the watch for a lens kind and returns its lister.
//
// It calls ensure, so it is the ROW path only. RowCount and Related must never
// reach it: drawing a sidebar badge or resolving a relationship has to stay
// free of side effects.
func (s *Store) lensLister(lk *lensKind, ns ...string) cache.GenericLister {
	s.ensure(lk.kind.Key, ns...)
	s.infMu.Lock()
	defer s.infMu.Unlock()
	desired := s.desiredInformerScope(lk.kind.Key, ns)
	scope := s.accessScopeLocked(lk.kind.Key, desired)
	return s.dynFactoryLocked(scope).ForResource(lk.gvr).Lister()
}

func packCollision(p lens.Pack, claimed map[string]bool) error {
	seen := map[string]bool{}
	for _, k := range p.Kinds {
		if strings.HasPrefix(k.Key, "cr|") {
			return fmt.Errorf("lens %q: kind key %q uses the reserved cr| prefix", p.Name, k.Key)
		}
		if claimed[k.Key] {
			return fmt.Errorf("lens %q: kind key %q is already taken", p.Name, k.Key)
		}
		if k.Short != "" && claimed[k.Short] {
			return fmt.Errorf("lens %q: kind %q alias %q is already taken", p.Name, k.Key, k.Short)
		}
		if seen[k.Key] || (k.Short != "" && seen[k.Short]) {
			return fmt.Errorf("lens %q: kind %q collides within its own pack", p.Name, k.Key)
		}
		seen[k.Key] = true
		if k.Short != "" {
			seen[k.Short] = true
		}
	}
	return nil
}

func compilePack(p lens.Pack) ([]*lensKind, error) {
	out := make([]*lensKind, 0, len(p.Kinds))
	for _, k := range p.Kinds {
		group, version, resource, err := lens.ParseGVR(k.GVR)
		if err != nil {
			return nil, fmt.Errorf("lens %q: kind %q: %w", p.Name, k.Key, err)
		}
		cols, err := compileColumns(p, k)
		if err != nil {
			return nil, fmt.Errorf("lens %q: kind %q: %w", p.Name, k.Key, err)
		}
		headers := make([]string, 0, len(k.Columns))
		for _, c := range k.Columns {
			headers = append(headers, c.Header)
		}
		out = append(out, &lensKind{
			kind: domain.Kind{
				Key:        k.Key,
				Name:       k.Name,
				Short:      k.Short,
				Group:      k.Group,
				Namespaced: k.Namespaced,
				Cols:       headers,
				Allowed:    append([]string(nil), k.Actions...),
			},
			gvr:  schema.GroupVersionResource{Group: group, Version: version, Resource: resource},
			pack: p,
			src:  k,
			cols: cols,
		})
	}
	return out, nil
}
