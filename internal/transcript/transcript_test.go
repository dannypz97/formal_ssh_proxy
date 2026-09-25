package transcript

import "testing"

func TestClean(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "ls -la\n",
			want:  "ls -la\n",
		},
		{
			name:  "ANSI color codes stripped",
			input: "\x1b[32mhello\x1b[0m",
			want:  "hello",
		},
		{
			name:  "backspace deletes prev char",
			input: "helo\x7fo",
			want:  "helo",
		},
		{
			name:  "Ctrl+C annotated",
			input: "abc\x03def",
			want:  "abc[Ctrl+C]def",
		},
		{
			name:  "Ctrl+Z annotated",
			input: "sleep 999\x1a",
			want:  "sleep 999[Ctrl+Z]",
		},
		{
			name:  "Ctrl+D annotated",
			input: "exit\x04",
			want:  "exit[Ctrl+D]",
		},
		{
			name:  "Ctrl+L annotated",
			input: "\x0cls",
			want:  "[Ctrl+L]ls",
		},
		{
			name:  "Ctrl+R annotated",
			input: "\x12search",
			want:  "[Ctrl+R]search",
		},
		{
			name:  "unknown control chars stripped",
			input: "abc\x07\x0bdef",
			want:  "abcdef",
		},
		{
			name:  "carriage return to newline",
			input: "line1\r\nline2",
			want:  "line1\nline2",
		},
		{
			name:  "tab preserved",
			input: "col1\tcol2",
			want:  "col1\tcol2",
		},
		{
			name:  "left/right cursor keys stripped",
			input: "text\x1b[D\x1b[Cmore",
			want:  "textmore",
		},
		{
			name:  "history keys annotated, normal and application mode",
			input: "\x1b[A\x1b[B\x1bOA > out\r",
			want:  "[Up][Down][Up] > out\n",
		},
		{
			name:  "bracketed paste markers stripped",
			input: "\x1b[200~rm -rf /\x1b[201~",
			want:  "rm -rf /",
		},
		{
			name:  "backspace removes whole UTF-8 character",
			input: "caf\xc3\xa9\x7fe",
			want:  "cafe",
		},
		{
			name:  "backspace does not cross a newline",
			input: "ls\n\x7f\x7fpwd",
			want:  "ls\npwd",
		},
		{
			name:  "parameterised sequences ending in A are not history keys",
			input: "ls\x1b[5A\x1b[1;5A\r",
			want:  "ls\n",
		},
		{
			name:  "cursor position reports (sent by the terminal) stripped",
			input: "\x1b[50;17Rls\r\x1b[51;17R\x7fcat /etc/shadow\r",
			want:  "ls\ncat /etc/shadow\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Clean(tt.input)
			if got != tt.want {
				t.Errorf("Clean(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
