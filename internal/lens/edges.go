package lens

import (
	"bytes"
	"strings"

	"k8s.io/client-go/util/jsonpath"
)

// EdgesFor splits the pack table into the edges LEAVING gvr and the edges
// ARRIVING at it.
//
// Edges are declared one-way but navigated both ways: from a pod you walk up
// to the CNPG Cluster that owns it, and from that Cluster back down to its
// pods. Only the declaration has a direction.
func EdgesFor(packs []Pack, gvr string) (out, in []Edge) {
	for _, p := range packs {
		for _, e := range p.Edges {
			if e.From == gvr {
				out = append(out, e)
			}
			if e.To == gvr {
				in = append(in, e)
			}
		}
	}
	return out, in
}

// Values returns the names on the far side that obj's near side points at.
//
// It is FORWARD only: obj is a `from` object and the result is the names of
// `to` objects. Reverse navigation is the same relation read the other way —
// "which from-objects name me?" — which cannot be answered from a single
// object, so the caller scans candidates and tests membership. Splitting it
// that way keeps this function pure and total.
//
// Several results are normal: an object may have several owners, and a
// wildcard path like .spec.routes[*].services[*].name yields one per route.
func Values(e Edge, obj map[string]any) []string {
	switch e.Via {
	case ViaLabel:
		return oneOf(nestedString(obj, "metadata", "labels", e.Key))
	case ViaAnnotation:
		return oneOf(nestedString(obj, "metadata", "annotations", e.Key))
	case ViaOwnerRef:
		return ownerNames(obj, e.To)
	case ViaField:
		return fieldValues(obj, e.Key)
	}
	return nil
}

func oneOf(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

func nestedString(obj map[string]any, path ...string) string {
	cur := any(obj)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[p]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

// ownerNames returns every ownerReference whose apiVersion matches the edge's
// target group/version. An ownerReference to some other kind is not this edge.
func ownerNames(obj map[string]any, to string) []string {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return nil
	}
	refs, ok := meta["ownerReferences"].([]any)
	if !ok {
		return nil
	}
	group, version, _, err := ParseGVR(to)
	if err != nil {
		return nil
	}
	wantAPI := version
	if group != "" {
		wantAPI = group + "/" + version
	}
	var out []string
	for _, r := range refs {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if api, _ := m["apiVersion"].(string); api != wantAPI {
			continue
		}
		if name, _ := m["name"].(string); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// fieldValues evaluates the edge key as a JSONPath on the from object.
//
// A FRESH jsonpath is parsed per call, deliberately not sharing the row
// builder's compiled paths: those are not safe for concurrent use, and edges
// resolve from a tea.Cmd goroutine. This runs once per keypress rather than
// per frame, so parsing here costs nothing that matters.
func fieldValues(obj map[string]any, path string) []string {
	jp := jsonpath.New("edge")
	jp.AllowMissingKeys(true)
	if err := jp.Parse(Braced(path)); err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := jp.Execute(&buf, obj); err != nil {
		return nil
	}
	// A wildcard path renders its matches space-separated.
	var out []string
	for _, f := range strings.Fields(buf.String()) {
		if f != "" && f != "<nil>" {
			out = append(out, f)
		}
	}
	return out
}
