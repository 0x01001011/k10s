package k8s

// CellLevel reports the severity a lens pack declares for one rendered cell,
// as "ok", "warn", "error", "unknown", or "" for a cell no pack grades.
//
// It reads only the header and the severity table off the compiled column —
// never the jsonpath, which is not safe to share with the render goroutine.
// An empty value is ungraded on purpose: an absent optional status field is
// not a status, and grading it would paint half of every CNPG table.
func (s *Store) CellLevel(kind, column, value string) string {
	if value == "" {
		return ""
	}
	lk, ok := s.lensKindFor(kind)
	if !ok {
		return ""
	}
	for _, c := range lk.cols {
		if c.header != column {
			continue
		}
		if !c.hasSev {
			return ""
		}
		return c.sev.Level(value).String()
	}
	return ""
}
