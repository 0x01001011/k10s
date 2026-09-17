package domain

import "strings"

// OwnerLabel turns an ownerReference name into the label a group header shows
// (T42).
//
// A pod's ownerReferences point at its ReplicaSet, not its Deployment, and
// `web-frontend-6b8c7d9f5` is not a name anyone thinks in. Kubernetes builds
// that name as `<deployment>-<pod-template-hash>`, so the Deployment is
// recoverable from the string alone — no second informer, no extra request,
// and in particular no watch on ReplicaSets that opening the Pods table
// deliberately does not start.
//
// It is a derivation, not a lookup, so it only speaks when the shape matches.
// A name whose last segment is not a plausible pod-template-hash comes back
// untouched: a confident wrong owner is worse than an unfamiliar one, and an
// operator shown a ReplicaSet name can still tell what it is.
func OwnerLabel(owner string) (label, hash string) {
	i := strings.LastIndexByte(owner, '-')
	if i <= 0 || i == len(owner)-1 {
		return owner, ""
	}
	suffix := owner[i+1:]
	if !isTemplateHash(suffix) {
		return owner, ""
	}
	return owner[:i], suffix
}

// isTemplateHash reports whether s looks like a pod-template-hash.
//
// Kubernetes generates these from an alphabet that omits vowels and
// easily-confused characters (bcdfghjklmnpqrstvwxz2456789), which is what
// makes them distinguishable from a word. The length bound is the belt: real
// hashes are 6-10 characters, so `api-gateway` keeps its `gateway` and only a
// genuine hash is taken off.
func isTemplateHash(s string) bool {
	if len(s) < 6 || len(s) > 10 {
		return false
	}
	digits := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c >= 'a' && c <= 'z':
			// Vowels never appear in a generated hash; a segment carrying
			// one is a word.
			if strings.IndexByte("aeiou", c) >= 0 {
				return false
			}
		default:
			return false
		}
	}
	// An all-letter segment of the right length is still likelier a word than
	// a hash; a real one mixes in digits.
	return digits > 0
}
