// Package transcript converts raw session stdin logs into readable text.
package transcript

import "unicode/utf8"

// Notable control characters and their readable annotations.
var ctrlLabels = map[byte]string{
	0x01: "[Ctrl+A]",
	0x02: "[Ctrl+B]",
	0x03: "[Ctrl+C]",
	0x04: "[Ctrl+D]",
	0x05: "[Ctrl+E]",
	0x06: "[Ctrl+F]",
	0x0c: "[Ctrl+L]",
	0x0e: "[Ctrl+N]",
	0x0f: "[Ctrl+O]",
	0x10: "[Ctrl+P]",
	0x12: "[Ctrl+R]",
	0x15: "[Ctrl+U]",
	0x17: "[Ctrl+W]",
	0x1a: "[Ctrl+Z]",
	0x1c: "[Ctrl+\\]",
}

// historyKeys annotates Up/Down arrows, which recall shell history: the
// recalled command itself is never part of stdin, so the marker is the only
// sign that an earlier command was re-run. Other escape sequences (cursor
// movement, cursor position reports, ...) are dropped.
var historyKeys = map[byte]string{
	'A': "[Up]",
	'B': "[Down]",
}

// Clean turns raw session stdin into readable text: terminal escape
// sequences (cursor keys, bracketed-paste markers, cursor position reports)
// are stripped except Up/Down, backspaces are applied, Enter becomes a
// newline and notable control characters are annotated, e.g. [Ctrl+C].
func Clean(raw string) string {
	out := make([]byte, 0, len(raw))

	for i := 0; i < len(raw); i++ {
		ch := raw[i]

		if ch == 0x1b && i+1 < len(raw) {
			switch raw[i+1] {
			case '[': // CSI: ESC [ params... final byte in 0x40–0x7E
				start := i + 2
				i = start
				for i < len(raw) && (raw[i] < 0x40 || raw[i] > 0x7e) {
					i++
				}
				if i == start && i < len(raw) { // no parameters: a plain key
					out = append(out, historyKeys[raw[i]]...)
				}
			case 'O': // SS3: ESC O x (arrow/function keys in application mode)
				i += 2
				if i < len(raw) {
					out = append(out, historyKeys[raw[i]]...)
				}
			default: // other two-byte sequences (e.g. Alt+key)
				i++
			}
			continue
		}

		switch {
		case ch == '\r' && i+1 < len(raw) && raw[i+1] == '\n':
			// CRLF → single newline; the '\n' is emitted on the next iteration.
		case ch == '\n' || ch == '\r':
			out = append(out, '\n')
		case ch == '\t':
			out = append(out, '\t')
		case ch == 0x7f || ch == 0x08: // DEL / backspace
			out = eraseLastChar(out)
		case ch < 0x20:
			// Annotate notable control chars; skip the rest.
			if label, ok := ctrlLabels[ch]; ok {
				out = append(out, label...)
			}
		default:
			out = append(out, ch)
		}
	}

	return string(out)
}

// eraseLastChar removes the last UTF-8 character from the current line, as a
// terminal would on backspace. It never erases past a newline.
func eraseLastChar(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return b
	}
	_, size := utf8.DecodeLastRune(b)
	return b[:len(b)-size]
}
