package ui

import (
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Mouse hit-testing.
//
// This replaces bubblezone, whose Scan is quadratic: it removes each marker
// with `input = input[:start] + input[pos:]`, reallocating and copying the
// whole frame once per marker. At ~80 markers that was 92% of everything the
// UI allocated per frame (4.4 MB of 4.8 MB).
//
// Every mark in this package wraps a single line, so the frame is scanned one
// line at a time and each line is rewritten once.
//
// Markers are private CSI sequences — ESC [ <n> z — exactly as bubblezone
// encoded them. That form is what keeps lipgloss.Width from counting an id as
// visible text, and terminals ignore it if one ever escapes a scan.

const zoneTerm = 'z' // CSI final byte in the private 0x70-0x7E range

// zoneBounds is where a marked run ended up, in terminal cells.
type zoneBounds struct {
	x, y, w int
	ok      bool
}

// inBounds reports whether a mouse event landed on this zone.
func (z zoneBounds) inBounds(e tea.MouseMsg) bool {
	if !z.ok || z.w <= 0 {
		return false
	}
	return e.Y == z.y && e.X >= z.x && e.X < z.x+z.w
}

var (
	zoneMu  sync.RWMutex
	zoneMap = map[string]zoneBounds{}

	// Ids are interned to integers so a marker's body is digits only and
	// stays a well-formed CSI sequence whatever the id contains.
	zoneIDMu sync.RWMutex
	zoneNum  = map[string]int{}
	zoneName = map[int]string{}
)

// zoneMarker returns the escape sequence standing for id, assigning it a
// number on first use.
func zoneMarker(id string) string {
	zoneIDMu.RLock()
	n, ok := zoneNum[id]
	zoneIDMu.RUnlock()

	if !ok {
		zoneIDMu.Lock()
		if n, ok = zoneNum[id]; !ok {
			n = len(zoneNum) + 1
			zoneNum[id] = n
			zoneName[n] = id
		}
		zoneIDMu.Unlock()
	}
	return "\x1b[" + strconv.Itoa(n) + string(zoneTerm)
}

func zoneIDFor(n int) (string, bool) {
	zoneIDMu.RLock()
	defer zoneIDMu.RUnlock()
	id, ok := zoneName[n]
	return id, ok
}

// markZone wraps s so scanZones can find where it landed.
func markZone(id, s string) string {
	if id == "" || s == "" {
		return s
	}
	mk := zoneMarker(id)
	return mk + s + mk
}

// getZone returns the bounds recorded for id by the last scanZones.
func getZone(id string) zoneBounds {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	return zoneMap[id]
}

// scanZones strips every marker from frame and records where each marked run
// ended up. It returns the frame as it should actually be printed.
//
// Lines are independent: a marker opened on one line and closed on another is
// dropped rather than mis-recorded, which is safe because every mark in this
// package wraps exactly one line.
func scanZones(frame string) string {
	// A frame with no escapes at all carries no markers, but the zone table
	// must still be replaced: with colour off and a modal up nothing is
	// marked, and leaving the previous frame's zones in place would keep the
	// covered rows clickable underneath the modal.
	if !strings.Contains(frame, "\x1b[") {
		zoneMu.Lock()
		zoneMap = map[string]zoneBounds{}
		zoneMu.Unlock()
		return frame
	}

	found := make(map[string]zoneBounds)
	var out strings.Builder
	out.Grow(len(frame))

	y := 0
	rest := frame
	for {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			scanZoneLine(rest, y, &out, found)
			break
		}
		scanZoneLine(rest[:i], y, &out, found)
		out.WriteByte('\n')
		rest = rest[i+1:]
		y++
	}

	zoneMu.Lock()
	zoneMap = found
	zoneMu.Unlock()

	return out.String()
}

// zoneAt reports the marker number at the start of s, if s begins with one.
func zoneAt(s string) (num, width int, ok bool) {
	if !strings.HasPrefix(s, "\x1b[") {
		return 0, 0, false
	}
	i := 2
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	// Digits, then the private terminator — anything else is an ordinary
	// escape (a colour, say) and must be copied through untouched.
	if i == 2 || i >= len(s) || s[i] != zoneTerm {
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[2:i])
	if err != nil {
		return 0, 0, false
	}
	return n, i + 1, true
}

// scanZoneLine copies line into out with its markers removed, recording the
// cell span of each id it closes.
func scanZoneLine(line string, y int, out *strings.Builder, found map[string]zoneBounds) {
	if !strings.Contains(line, "\x1b[") {
		out.WriteString(line)
		return
	}

	open := map[int]int{} // marker number -> start cell

	// at is the cell the next emitted character lands on. Each emitted run is
	// measured once and added, rather than re-measuring the whole line prefix
	// per marker — that re-measuring was 20% of frame time.
	//
	// Runs are only ever cut immediately before a marker, so a run never ends
	// part-way through an escape sequence and ansi.StringWidth sees whole
	// sequences to skip.
	at := 0
	pos := 0  // start of the run not yet emitted
	from := 0 // where to look for the next marker

	for {
		i := strings.Index(line[from:], "\x1b[")
		if i < 0 {
			break
		}
		i += from

		n, w, isZone := zoneAt(line[i:])
		if !isZone {
			// An ordinary escape (a colour). Leave it in the run and keep
			// looking past its introducer so it is not rematched.
			from = i + 2
			continue
		}

		run := line[pos:i]
		out.WriteString(run)
		at += ansi.StringWidth(run)

		if start, isClose := open[n]; isClose {
			if id, ok := zoneIDFor(n); ok {
				found[id] = zoneBounds{x: start, y: y, w: at - start, ok: true}
			}
			delete(open, n)
		} else {
			open[n] = at
		}

		pos = i + w
		from = pos
	}

	out.WriteString(line[pos:])
}
