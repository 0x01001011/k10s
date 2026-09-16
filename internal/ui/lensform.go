package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
)

// lensFormField is one parameter as the operator is filling it in.
type lensFormField struct {
	spec domain.LensParamSpec
	// buf is what they have typed, which doubles as the value AND as the
	// filter. One buffer rather than two because they are the same thing:
	// typing "postgres" both narrows the list and is a prefix of the answer.
	buf string
	// filtered is recomputed on every keystroke, never in View. View runs on
	// every repaint while the form changes only when a key arrives, and the
	// package's perf guards exist to keep work off that path.
	filtered []domain.LensOption
	optIdx   int
}

// lensFormState is one open parameter form.
//
// It is a separate modal from confirmState rather than an extension of it. A
// confirm asks one question with two answers; this collects several values,
// each with its own suggestions, and re-renders a command preview as they
// change. Folding the second into the first would have given confirmState four
// more fields that mean nothing for the other five callers.
type lensFormState struct {
	kind, ns, name, short string
	sp                    domain.LensActionSpec

	fields []lensFormField
	focus  int

	// preview is the equivalent kubectl command for the CURRENT values,
	// already split into rendered rows.
	preview []string

	// typedBuf is the confirmation word, collected after the fields are
	// satisfied and only when the action asks for one.
	typedBuf string
	typing   bool
}

// openLensForm starts collecting an action's parameters.
func (m *Model) openLensForm(kind, ns, name, short string, sp domain.LensActionSpec) {
	st := &lensFormState{kind: kind, ns: ns, name: name, short: short, sp: sp}
	for _, p := range sp.Params {
		f := lensFormField{spec: p, buf: p.Default}
		f.refilter()
		st.fields = append(st.fields, f)
	}
	m.lensForm = st
	m.refreshLensPreview()
}

// params is the form's current answers, in the shape LensAction wants.
func (st *lensFormState) params() map[string]string {
	out := make(map[string]string, len(st.fields))
	for _, f := range st.fields {
		out[f.spec.Name] = f.buf
	}
	return out
}

// refilter narrows the suggestions to what the buffer matches.
//
// A buffer that exactly equals an option is treated as "no filter typed yet":
// having just picked postgresql-2, the operator should still see its siblings
// rather than a list of one, so they can change their mind by arrowing.
func (f *lensFormField) refilter() {
	q := strings.ToLower(strings.TrimSpace(f.buf))
	exact := false
	for _, o := range f.spec.Options {
		if strings.EqualFold(o.Value, f.buf) {
			exact = true
			break
		}
	}
	if q == "" || exact {
		f.filtered = f.spec.Options
		f.optIdx = 0
		for i, o := range f.spec.Options {
			if strings.EqualFold(o.Value, f.buf) {
				f.optIdx = i
			}
		}
		return
	}
	f.filtered = nil
	for _, o := range f.spec.Options {
		if strings.Contains(strings.ToLower(o.Value), q) {
			f.filtered = append(f.filtered, o)
		}
	}
	f.optIdx = 0
}

// valid reports whether this field's value may be submitted.
func (f lensFormField) valid() bool {
	if f.buf == "" {
		return !f.spec.Required
	}
	if f.spec.AllowFree || len(f.spec.Options) == 0 {
		return true
	}
	for _, o := range f.spec.Options {
		if o.Value == f.buf {
			return true
		}
	}
	return false
}

// ready reports whether every field is satisfied.
func (st *lensFormState) ready() bool {
	for _, f := range st.fields {
		if !f.valid() {
			return false
		}
	}
	return true
}

// needsTyping reports whether this action still wants a word typed out.
func (st *lensFormState) needsTyping() bool { return st.sp.Confirm == "typed" }

// confirmWant is the word the typed gate demands.
//
// The pack's confirmValue is a one-variable template, expanded here rather than
// round-tripped through the backend: the value is already in this form, and a
// gate that has to wait for a round trip is a gate that lags the field it
// guards. Anything but a {{.Params.x}} reference falls back to the object's
// name, which is what every pack without a confirmValue relies on.
func (st *lensFormState) confirmWant() string {
	tpl := strings.TrimSpace(st.sp.ConfirmValue)
	const prefix, suffix = "{{.Params.", "}}"
	if strings.HasPrefix(tpl, prefix) && strings.HasSuffix(tpl, suffix) {
		want := st.params()[strings.TrimSuffix(strings.TrimPrefix(tpl, prefix), suffix)]
		if want != "" {
			return want
		}
	}
	return st.name
}

// armed reports whether Enter would actually fire the write.
func (st *lensFormState) armed() bool {
	if !st.ready() {
		return false
	}
	if !st.needsTyping() {
		return true
	}
	return st.typing && st.typedBuf == st.confirmWant()
}

// refreshLensPreview recomputes the command for the values now in the form.
//
// On every change rather than on submit: the preview is how an operator checks
// that the button does what they think, so one that lags the field describes a
// mutation other than the one about to happen.
func (m *Model) refreshLensPreview() {
	st := m.lensForm
	if st == nil {
		return
	}
	lv, ok := m.src.(domain.LensVerbs)
	if !ok {
		return
	}
	cmd := lv.LensPreview(st.kind, st.ns, st.name, st.sp.ID, st.params())
	st.preview = nil
	if cmd != "" {
		st.preview = strings.Split(cmd, "\n")
	}
}

// handleLensFormKey drives the form. It owns the keyboard completely while
// open: every letter belongs to a field, so no single-key shortcut survives
// here.
func (m *Model) handleLensFormKey(msg tea.KeyMsg) tea.Cmd {
	st := m.lensForm
	if st == nil {
		return nil
	}
	key := msg.String()

	if st.typing {
		return m.handleLensFormTypedKey(key)
	}

	switch key {
	case "esc":
		m.lensForm = nil
		m.toast = "cancelled"
		return nil

	case "tab", "shift+tab":
		if len(st.fields) == 0 {
			return nil
		}
		step := 1
		if key == "shift+tab" {
			step = -1
		}
		st.focus = (st.focus + step + len(st.fields)) % len(st.fields)
		return nil

	case "up", "down":
		f := &st.fields[st.focus]
		if len(f.filtered) == 0 {
			return nil
		}
		if key == "down" {
			f.optIdx = (f.optIdx + 1) % len(f.filtered)
		} else {
			f.optIdx = (f.optIdx - 1 + len(f.filtered)) % len(f.filtered)
		}
		return nil

	case "enter":
		return m.lensFormEnter()

	case "backspace":
		f := &st.fields[st.focus]
		if b := []rune(f.buf); len(b) > 0 {
			f.buf = string(b[:len(b)-1])
			f.refilter()
			m.refreshLensPreview()
		}
		return nil

	default:
		if r := []rune(key); len(r) == 1 {
			f := &st.fields[st.focus]
			f.buf += key
			f.refilter()
			m.refreshLensPreview()
		}
		return nil
	}
}

// lensFormEnter takes the highlighted suggestion, advances, or submits.
func (m *Model) lensFormEnter() tea.Cmd {
	st := m.lensForm
	if len(st.fields) > 0 {
		f := &st.fields[st.focus]
		// Accept the highlighted suggestion unless it is already the value —
		// pressing Enter twice on a chosen option should move on, not sit
		// there re-choosing it.
		if len(f.filtered) > 0 && f.optIdx < len(f.filtered) && f.filtered[f.optIdx].Value != f.buf {
			f.buf = f.filtered[f.optIdx].Value
			f.refilter()
			m.refreshLensPreview()
			return nil
		}
		if st.focus < len(st.fields)-1 {
			st.focus++
			return nil
		}
	}
	if !st.ready() {
		m.toast = "✗ " + st.unreadyWhy()
		return nil
	}
	if st.needsTyping() && !st.typing {
		st.typing = true
		return nil
	}
	return m.submitLensForm()
}

// handleLensFormTypedKey collects the confirmation word.
func (m *Model) handleLensFormTypedKey(key string) tea.Cmd {
	st := m.lensForm
	switch key {
	case "esc":
		// Back to the fields rather than out of the form: an operator who
		// mistyped the word has not changed their mind about the action.
		st.typing, st.typedBuf = false, ""
		return nil
	case "enter":
		if !st.armed() {
			m.toast = "type " + st.confirmWant() + " to confirm"
			return nil
		}
		return m.submitLensForm()
	case "backspace":
		if b := []rune(st.typedBuf); len(b) > 0 {
			st.typedBuf = string(b[:len(b)-1])
		}
		return nil
	default:
		if r := []rune(key); len(r) == 1 {
			st.typedBuf += key
		}
		return nil
	}
}

// unreadyWhy names the first field standing in the way.
func (st *lensFormState) unreadyWhy() string {
	for _, f := range st.fields {
		if f.valid() {
			continue
		}
		if f.buf == "" {
			return f.spec.Label + " is required"
		}
		return f.spec.Label + ": " + f.buf + " is not one of its allowed values"
	}
	return "the form is incomplete"
}

// wrapPreview wraps one line of a command, KEEPING its leading whitespace.
//
// wrapNotice splits on Fields, which is right for prose and wrong here: a
// create action's preview is an indented JSON manifest, and collapsing the
// indentation flattens every nested key to the same level. A reader checking
// whether `targetTime` sits under `recoveryTarget` or beside it then cannot
// tell, which is the one question the preview is there to answer.
func wrapPreview(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	indent := s[:len(s)-len(strings.TrimLeft(s, " "))]
	if lipgloss.Width(s) <= width {
		return []string{s}
	}
	// Continuation lines are indented one step further than the line they
	// continue, so a wrapped value is visibly not a new key.
	cont := indent + "  "
	var (
		out  []string
		line = indent
	)
	for _, w := range strings.Fields(s) {
		switch {
		case strings.TrimSpace(line) == "":
			line += w
		case lipgloss.Width(line)+1+lipgloss.Width(w) <= width:
			line += " " + w
		default:
			out = append(out, line)
			line = cont + w
		}
		// A word longer than the box is not hypothetical here: JSON values
		// have no spaces, so a long cluster name or an object-store URL is one
		// token. Wrapping only at spaces would push it straight through the
		// border — the same overflow the confirm modal was fixed for.
		for lipgloss.Width(line) > width {
			cut := width
			r := []rune(line)
			if cut > len(r) {
				cut = len(r)
			}
			out = append(out, string(r[:cut]))
			line = cont + string(r[cut:])
		}
	}
	if strings.TrimSpace(line) != "" {
		out = append(out, line)
	}
	return out
}

// lensFormMaxOptions caps the suggestion list. A cluster with two hundred
// Backups would otherwise push the preview and the buttons off the screen —
// and the typeahead is the way through a long list, not scrolling it.
const lensFormMaxOptions = 6

// overlayLensForm draws the open parameter form.
func (m *Model) overlayLensForm(root Block) Block {
	th := m.th()
	st := m.lensForm
	accent := th.Accent
	if st.needsTyping() {
		accent = th.Err
	}

	w := 62
	if w > m.w-8 {
		w = m.w - 8
	}
	inner := w - 2
	textW := inner - 4

	body := []string{"", paint(th.Bg, th.Subtle, false, "  "+trunc(st.short+"/"+st.name+" · ns "+st.ns, textW)), ""}

	for i, f := range st.fields {
		body = append(body, m.lensFormField(i, f, textW)...)
	}

	if len(st.preview) > 0 {
		body = append(body, "")
		// Wrapped, not truncated. The preview exists to be read and checked
		// against; a command ending in an ellipsis hides the half that says
		// which instance it names.
		for _, ln := range st.preview {
			for _, seg := range wrapPreview(ln, textW) {
				body = append(body, paint(th.Bg, th.Subtle, false, "  "+seg))
			}
		}
	}

	if st.typing {
		body = append(body, "",
			paint(th.Bg, th.Subtle, false, "  type "),
			paint(th.Bg, accent, true, "  "+trunc(st.confirmWant(), textW)),
		)
		col := th.Fg
		if st.armed() {
			col = th.Ok
		}
		body = append(body, paint(th.Bg, th.Border, false, "  ▸ ")+
			paint(th.Bg, col, false, trunc(st.typedBuf+"▏", textW-2)))
	}

	body = append(body, "", m.lensFormButtons(inner, accent, st.armed()), "")

	h := len(body) + 2
	if h > m.h-2 {
		h = m.h - 2
	}
	box := Panel(th, PanelOpts{
		Title: "⚙  " + st.sp.Label, Focused: true, W: w, H: h, BorderCol: accent,
	}, body)
	return root.Overlay(box, (m.w-w)/2, maxi(0, (m.h-h)/2))
}

// lensFormField renders one labelled field plus, when focused, its filtered
// suggestions.
//
// Only the focused field shows its list. Showing every list at once would make
// a two-field form taller than the terminal, and the unfocused fields are
// already answered — their value is the useful part, not their alternatives.
func (m *Model) lensFormField(i int, f lensFormField, textW int) []string {
	th := m.th()
	st := m.lensForm
	focused := i == st.focus && !st.typing

	marker, labelCol := "   ", th.Subtle
	if focused {
		marker, labelCol = " ▸ ", th.Accent
	}
	label := f.spec.Label
	if f.spec.Required && f.buf == "" {
		label += " *"
	}

	value := f.buf
	if focused {
		value += "▏"
	}
	valCol := th.Fg
	if !f.valid() {
		valCol = th.Err
	}
	if f.buf == "" && !focused {
		value, valCol = "—", th.Subtle
	}

	// Padded, not just truncated: trunc only shortens, so a short label left
	// the value butted straight against it ("instancepostgres").
	const labelW = 14
	label = trunc(label, labelW)
	label += spaces(maxi(1, labelW-lipgloss.Width(label)))

	out := []string{
		paint(th.Bg, labelCol, focused, marker+label) +
			paint(th.Bg, valCol, false, trunc(value, textW-labelW-3)),
	}
	if !focused {
		return out
	}

	if len(f.filtered) == 0 {
		why := f.spec.OptionsNote
		if why == "" && len(f.spec.Options) > 0 {
			why = "nothing matches"
		}
		if why != "" {
			out = append(out, paint(th.Bg, th.Subtle, false, "     "+trunc(why, textW-3)))
		}
		return out
	}

	shown := f.filtered
	if len(shown) > lensFormMaxOptions {
		shown = shown[:lensFormMaxOptions]
	}
	for j, o := range shown {
		bullet, col, bold := "   ", th.Fg, false
		if j == f.optIdx {
			bullet, col, bold = " ▸ ", th.Accent, true
		}
		row := paint(th.Bg, th.Border, false, "  "+bullet) +
			paint(th.Bg, col, bold, trunc(o.Value, textW-20))
		if o.Note != "" {
			row += paint(th.Bg, th.Subtle, false, "  "+trunc(o.Note, 14))
		}
		out = append(out, row)
	}
	if n := len(f.filtered) - len(shown); n > 0 {
		out = append(out, paint(th.Bg, th.Subtle, false,
			"     "+strconv.Itoa(n)+" more — keep typing to narrow"))
	}
	return out
}

// lensFormButtons is the confirm/cancel row.
func (m *Model) lensFormButtons(inner int, accent lipgloss.Color, armed bool) string {
	th := m.th()
	okPlain, noPlain := "  Enter · Confirm  ", "  Esc · Cancel  "
	okBG := accent
	if !armed {
		// Genuinely inert until the form is satisfied. A button that looks
		// live and refuses the keypress is worse than one that looks dead.
		okBG = th.Border
	}
	ok := markZone("lf:ok", lipgloss.NewStyle().Background(okBG).Foreground(th.Bg).Bold(true).Render(okPlain))
	no := markZone("lf:no", lipgloss.NewStyle().Background(th.Border).Foreground(th.Fg).Render(noPlain))
	pre := (inner - len(okPlain) - len(noPlain) - 2) / 2
	if pre < 1 {
		pre = 1
	}
	return paint(th.Bg, th.Bg, false, spaces(pre)) + ok + paint(th.Bg, th.Bg, false, spaces(2)) + no
}

// submitLensForm closes the form and performs the write.
func (m *Model) submitLensForm() tea.Cmd {
	st := m.lensForm
	if st == nil {
		return nil
	}
	m.lensForm = nil
	return m.runLensAction(st.kind, st.ns, st.name, st.short, st.sp, st.params())
}
